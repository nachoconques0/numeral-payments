# Numeral Payments

A small payment service: it accepts one payment request over HTTP, validates it against the
provided JSON schema, stores it in SQLite, deposits a payment file in the bank folder, and
updates the payment status when the bank drops its response file back.

## Architecture

The bank is a **port with pluggable adapters**. One adapter is one complete bank integration:
how a payment is deposited, and how that bank's responses are read back.

```
                                        ┌── xmlbank  (pain.008.002.02 XML  +  CSV responses)
HTTP ──► Controller ──► Service ──► BankAdapter ─┤── csvbank  (flat CSV instruction +  CSV responses)
                            │                    └── ... a new bank is a new package
                            ▼
                        Repository ──► SQLite
                            ▲
                            │
                    Poller ──► polls BANK_FOLDER for the adapter's response files
```

The response poller does not touch the repository: it calls the service back through a one-method
interface, so the status update goes through the same business layer as everything else.

### Layers

| Layer | Package | Responsibility |
|---|---|---|
| Entry point | `cmd/server` | Load config, build the app, run it |
| Lifecycle | `internal/app` | Wire dependencies, start response poller + HTTP server, graceful shutdown |
| Delivery | `internal/controller/payment`, `internal/http` | Decode, schema-validate, map errors to status codes |
| Business | `internal/service/payment` | Idempotency, ordering of the write and the deposit |
| Persistence | `internal/repository/payment` | SQL only |
| Domain | `internal/entity/payment` | `Payment`, `Status`, invariants. No JSON, no SQL, no tags |
| DTOs | `internal/model` | Wire structs with JSON tags, and the mappers to/from entities |
| Bank port | `internal/bank`, `internal/bank/{xmlbank,csvbank}` | Deposit and response parsing per bank |
| Response poller | `internal/bank/response` | Poll the folder, hand statuses to the service |

Each layer defines the interface it needs from the layer below (the controller declares
`Service`, the service declares `Repository` and `BankAdapter`, the poller declares
`ResponseSource` and `StatusUpdater`), so dependencies point inwards and every layer is
testable with a hand-written fake.

## End-to-end flow

1. `POST /payments` arrives with HTTP basic auth.
2. Auth middleware compares both credentials in constant time; a mismatch is `401`.
3. The raw body is read (capped at 1 MB) and validated against the embedded
   `resources/request_schema.json`. Any violation is `400` with the list of violations.
4. The amount is read from its decimal text into integer cents; more than two decimals, exponent
   notation, or an amount above the 999,999,999.99 ceiling is `400`.
5. If the idempotency key is already stored, the stored payment is returned when it is the same
   payment, and `409` when the key was reused for a different one.
6. Otherwise: **insert the row as `PENDING`, then write the payment file.** If the deposit
   fails, the row is marked `FAILED`.
7. The response is `200` with the payment and its status.
8. The bank later drops a CSV response in the bank folder.
9. The response poller notices it, waits until it has stopped changing, parses it, and asks the
   service to apply each status.
10. A `PENDING` row flips to `PROCESSED` or `REJECTED` and the file moves to `processed/`. Any
    row that could not be applied sends the file to `failed/` instead.

## Running it

```bash
export BANK_FOLDER=./data/bank
export SQLITE_DB_FILE_LOCATION=./data/payments.db
make run            # or: go run ./cmd/server
```

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `BANK_FOLDER` | yes | – | Folder shared with the bank |
| `SQLITE_DB_FILE_LOCATION` | yes | – | SQLite file path |
| `ADDR` | no | `:8080` | Listen address |
| `AUTH_USERNAME` | no | `CALCAGNO` | Basic auth user |
| `AUTH_PASSWORD` | no | `xxxx` | Basic auth password |
| `POLL_INTERVAL` | no | `2s` | Bank folder poll interval |
| `BANK_ADAPTER` | no | `xml` | `xml` or `csv`; anything else fails at startup |

### Send a payment

```bash
curl -i -u CALCAGNO:xxxx -X POST http://localhost:8080/payments \
  -H 'Content-Type: application/json' \
  -d @resources/request_sample.json
```

```json
{"idempotency_unique_key":"JXJ984XXXZ","status":"PENDING","amount":"42.99","currency":"EUR","created_at":"..."}
```

A `payment_<internal-id>.xml` file appears in `BANK_FOLDER` — for the first payment,
`payment_1.xml`. The filename uses the internal row id; the idempotency key you sent travels
inside the file as the `MsgId`, which is what the bank correlates its response on.

### Simulate the bank's response

Either run the bundled bank simulator, which watches for payment files and answers a few
seconds later:

```bash
make fakebank                      # answers PROCESSED
go run ./cmd/fakebank -status REJECTED
```

or drop a response by hand:

```bash
cp resources/bank_response.csv $BANK_FOLDER/
```

Within a poll interval or two — the file has to look stable first — the payment status becomes
`PROCESSED` and the CSV moves to `$BANK_FOLDER/processed/`.

## Postman collection

`postman_collection.json` at the repo root is a Postman v2.1.0 collection covering every
behaviour below. Import it with **File > Import** and point it at a running service.

`baseUrl` (`http://localhost:8080`), `username`, `password` and `idempotencyKey` are collection
variables — change `baseUrl` if you run on another port. Basic auth is set on the collection, so
every request inherits it except the two in **Auth** that deliberately override it.

Run the folders in order: **Health**, **Payments**, **Idempotency**, **Auth**, **Validation**,
**Method not allowed**. Each request asserts its expected status code. The keys are fixed on
purpose: re-running *Create payment* returns the payment already stored rather than creating a
second one, and *Conflict* only returns `409` once *Create payment* has run. *Create payment
(random key)* generates a fresh key each time if you want a new payment on every send.

## API

`POST /payments` — HTTP basic auth required. Body: the payment request from the provided schema.

| Status | When |
|---|---|
| `200` | Stored and deposited with the bank, **or** a replay of the same payment under a key already stored |
| `400` | Malformed JSON, the body violates the payment schema (violations listed in `details`), or the amount has more than two decimals, uses exponent notation, or exceeds the 999,999,999.99 ceiling |
| `401` | Missing or wrong credentials (`WWW-Authenticate: Basic realm="numeral"`) |
| `409` | The idempotency key is already stored against a different payment |
| `405` | Known path, unsupported method |
| `415` | `Content-Type` is present and is not `application/json` |
| `500` | The payment could not be stored, or could not be deposited with the bank (the row is left `FAILED`) |

### Payment statuses

| Status | Meaning |
|---|---|
| `PENDING` | Stored, and the payment file written, awaiting the bank's response. See the ordering trade-off below |
| `PROCESSED` | The bank accepted the payment |
| `REJECTED` | The bank refused the payment |
| `FAILED` | Recorded, but the deposit failed. Not a status the bank reports |

Errors share one shape:

```json
{"code":400,"message":"the request does not match the payment schema","details":["..."]}
```

`GET /health` is open and returns `{"status":"ok"}`.

## Design choices

**The bank is a port, not a hard-coded file format.** `BankAdapter` covers deposit,
response pattern and response parsing — everything that varies between banks. `xmlbank` is the
bank in the exercise; `csvbank` is a second bank that takes flat CSV instructions. Both run
today, selected by `BANK_ADAPTER`, and the service does not know the difference.

**`ResponsePattern()` lives on the port for a real reason.** `xmlbank` deposits `.xml`, so it can
safely watch `*.csv`. `csvbank` deposits `.csv`, so it watches `response_*.csv` and does not read
its own deposits back. The poller does not know that; the adapter does.

**Amounts are integer cents, parsed from text.** The DTO takes the amount as `json.Number` and
reads its digits into cents, so the value does not pass through a float in either direction.
`1.15` and `0.29` are not exactly representable in binary floating point, so a float round trip
can land a cent off; reading the literal cannot. An amount with more than two decimals, or in
exponent notation, is rejected with `400` rather than quietly rounded — `42.999` is a client bug
worth reporting, not an amount to guess at.

The domain also caps a payment at **999,999,999.99**, the SEPA credit transfer ceiling, so an
absurd amount is refused with `400` instead of being accepted because it happens to fit in an
`int64`. The `int64` overflow guard stays behind it as defence in depth, keeping the parser safe
independently of the business rule. A production system would make the ceiling configurable per
currency and per bank agreement rather than hard-coding one scheme's limit.

**Payment file names use the internal row id, not the idempotency key.** The schema constrains
the key only by length, so a key like `../../../a` is schema-valid. Filenames are
`payment_<id>.xml`, and `WriteFileAtomic` additionally refuses any name that resolves outside the
bank folder. The key still travels inside the file as `MsgId`, which is what the bank correlates
on, so the response flow is unchanged.

**Record the intent, then act.** SQLite and the filesystem are separate resources, so there is no
cross-resource transaction here and the design does not claim one. The row is committed as
`PENDING` first, then the payment file is written, and a deposit that fails is recorded as
`FAILED`.

The ordering picks which way the operation fails. A known failure leaves a `FAILED` row and no
payment file — visible and queryable. A process crash between the commit and the deposit leaves a
`PENDING` row whose file was never written: an ambiguous state that this exercise leaves for a
human to inspect, and that a production system would reconcile against the bank.

**`MsgId` is the idempotency key.** That is the only identifier the bank echoes in its response
CSV, so it is what correlates a response back to a payment. Using anything else would break the
correlation.

**A repeated idempotency key is checked, not trusted.** Replaying the same key with the same
debtor, creditor, amount and currency returns the stored payment and deposits nothing further.
Replaying it with a *different* payment returns `409`, because silently answering `200` with
someone else's payment is the more dangerous behaviour. The key is `UNIQUE` in the database, so
concurrent requests cannot create two rows for one key: the loser of the constraint loads the
winner's payment and reaches the same verdict. The preliminary lookup is only an optimisation.

**Validation uses the provided schema file, embedded.** `resources/request_schema.json` is
compiled with `santhosh-tekuri/jsonschema` and embedded with `go:embed`, so the file we validate
against is literally the file we were given. No hand-written checks to drift from it, and no
Gin binding tags duplicating the same rules.

**Polling with a stability check, not fsnotify.** The poller ignores any file whose mtime is
newer than one poll interval, which covers the ordinary case of a response still being written.
It is a heuristic, not a guarantee: a bank that stalls mid-write for longer than the interval
would still be read early. For the exercise that is an accepted assumption; a real integration
would agree a completion protocol with the bank, such as writing to a temporary name and
renaming it when the file is complete. `cmd/fakebank` does exactly that.

**Quarantine folders, and nothing unresolved is filed as success.** A file moves to `processed/`
when every row was applied — including a row that was already applied, since re-sending the same
response is a safe no-op. Anything else — an unparseable file, an unknown payment ID, a status the
bank may not report, or a response contradicting a terminal status — moves to `failed/` with a log
line naming the rows, so it stays visible instead of looking handled. Valid rows in the same file
are still applied first. Moving files out of the polled folder stops ordinary redelivery, and
because the status transition is a single conditional `UPDATE`, re-processing a file is safe.

**`database/sql` and `modernc.org/sqlite`, not an ORM.** The service runs three queries; an ORM
would be more machinery than logic. The driver is pure Go, so there is no cgo and the binary
builds anywhere. The `Repository` interface means swapping the storage is a one-file change.

**Env-only configuration.** The exercise mandates `BANK_FOLDER` and `SQLITE_DB_FILE_LOCATION`,
so the environment is the source of truth. Both are required and the service fails fast with a
message naming what is missing.

**Gin for the delivery layer**, standard library everywhere else. Three direct dependencies in
total: Gin, the schema validator, the SQLite driver.

**The debtor's account element is `CdtrAcct`, not `DbtrAcct`.** The provided `payment.xsd`
defines `DbtrType` with a `CdtrAcct` child, and the provided `payment_sample.xml` does the same.
ISO 20022 would normally call it `DbtrAcct`; we conform to the schema we were given rather than
to the standard, and the generated file matches the sample exactly.

## Tests

```bash
make test    # go test ./...
```

Hand-written fakes rather than a mocking framework: the interfaces are small, so a fake is
shorter than the generated code and needs no toolchain to be installed. Tests live in
external `_test` packages, so they only see each package's public surface.

## Trade-offs and what I would do next

- **No retry or reconciliation.** A response file that fails to parse lands in `failed/`, and a
  `FAILED` payment stays failed, both waiting for a human. A production system would re-deposit
  `FAILED` payments from a durable job, reconcile `PENDING` rows against the bank rather than
  assuming they failed, and alert on both. A separate deposit lifecycle
  (`DEPOSIT_PENDING → SUBMITTED → PROCESSED`) would avoid overloading `PENDING` for two meanings.
- **The response poller assumes a single node.** Two replicas polling the same folder would race for the
  same response file. Real deployments would use a queue, or leader election, instead of a shared folder.
- **Basic auth over plain HTTP** assumes TLS terminates upstream. Credentials come from the
  environment; a real service would use a secret manager and per-client credentials.
- **SQLite because the exercise asks for it.** The write path is serialised to one connection,
  which is fine here and would not survive real volume; Postgres is the obvious next step and
  the `Repository` interface is where that swap happens.
- **No `GET /payments/{key}` and no pagination.** The service reads payments back internally but
  does not expose them yet; that endpoint is the obvious next addition.
- **Logs but no metrics.** Structured logs via `log/slog`; deposit latency, poller lag and
  status counts would want real metrics.
- **The XML is validated against the XSD by eye, not in code.** Go has no XSD validator in the
  standard library; the generated file was diffed against the provided sample instead.

## Layout

```
postman_collection.json         Postman v2.1.0 collection for every endpoint and error case
cmd/server/main.go              entry point: load config, build app, run
cmd/fakebank/main.go            bank simulator for demos
internal/app/app.go             dependency wiring, poller + server lifecycle, graceful shutdown
internal/config/config.go       environment settings and validation
internal/http/router.go         routes
internal/http/middleware/auth.go  constant-time HTTP basic auth
internal/controller/payment/    HTTP handler for POST /payments
internal/service/payment/       business logic, idempotency, BankAdapter port
internal/repository/payment/    SQLite persistence
internal/entity/payment/        pure domain entity, statuses, invariants
internal/model/payment.go       request/response DTOs and entity mappers
internal/bank/bank.go           shared response type, temp-file write, response CSV parsing
internal/bank/xmlbank/          pain.008.002.02 adapter
internal/bank/csvbank/          flat CSV adapter
internal/bank/response/         bank folder poller
internal/validator/             JSON schema validation
internal/errors/errors.go       AppError and its HTTP mapping
internal/db/sqlite.go           SQLite connection
resources/                      the files provided with the exercise, embedded
```
