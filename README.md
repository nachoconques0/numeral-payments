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
   notation, or an amount too large to store is `400`.
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
every request inherits it except `06` and `07`, which override it deliberately.

The requests are a flat list numbered in run order, because they build on each other: `02`
creates the payment that `04` replays and `05` conflicts with. Each request asserts its expected
status code, so **Run collection** goes green top to bottom.

The keys are fixed on purpose: re-running `02` returns the payment already stored rather than
creating a second one, and `05` only returns `409` once `02` has run. Use `03` if you want a
fresh payment on every send.

## API

`POST /payments` — HTTP basic auth required. Body: the payment request from the provided schema.

| Status | When |
|---|---|
| `200` | Stored and deposited with the bank, **or** a replay of the same payment under a key already stored |
| `400` | Malformed JSON, the body violates the payment schema (violations listed in `details`), or the amount has more than two decimals, uses exponent notation, or is too large to store |
| `401` | Missing or wrong credentials (`WWW-Authenticate: Basic realm="numeral"`) |
| `409` | The idempotency key is already stored against a different payment |
| `405` | Known path, unsupported method |
| `415` | `Content-Type` is present and is not `application/json` |
| `500` | The payment could not be stored, or could not be deposited with the bank (the row is left `FAILED`) |

### Payment statuses

| Status | Meaning |
|---|---|
| `PENDING` | Recorded and awaiting the bank's response. On the normal creation path the payment file has been deposited; see the creation trade-off below |
| `PROCESSED` | The bank accepted the payment |
| `REJECTED` | The bank refused the payment |
| `FAILED` | Recorded, but the deposit failed. Not a status the bank reports |

Errors share one shape:

```json
{"code":400,"message":"the request does not match the payment schema","details":["..."]}
```

`GET /health` is open and returns `{"status":"ok"}`.

## Design choices

**The bank is a port with adapters.** `BankAdapter` is the only thing that knows a bank's file
format: how a payment is deposited, where that bank's responses appear, and how to read them. The
service never sees XML or CSV. `xmlbank` is the bank in the exercise; `csvbank` is a second bank
taking flat CSV instructions, selected by `BANK_ADAPTER`, and adding a third would not change the
service.

**Idempotency is enforced by the database.** `idempotency_unique_key` is `UNIQUE`, so two
concurrent requests with one key cannot create two rows — the loser of the constraint loads the
winner's payment. The preliminary lookup is only an optimisation. Replaying a key with the same
debtor, creditor, amount and currency returns the stored payment and deposits nothing further;
replaying it with a *different* payment is `409`, because answering `200` with someone else's
payment is the more dangerous behaviour.

**Money is integer cents, parsed from decimal text.** The DTO takes the amount as `json.Number`
and reads its digits into cents, so the value does not pass through a `float64` in either
direction — `1.15` and `0.29` are not exactly representable in binary floating point, and a float
round trip can land a cent off. This service settles in EUR and so assumes two minor-unit digits:
`42.999` is rejected with `400` rather than quietly rounded. An amount too large for `int64` cents
is rejected for the same reason.

**The bank response transition is one conditional `UPDATE`.** The status moves with
`... WHERE idempotency_unique_key = ? AND status = 'PENDING'`, and the affected row count decides
what happened. That avoids a read-then-write race between concurrent response handlers: only one
terminal transition can win. Re-sending the same response is a safe no-op, and a response
contradicting an existing terminal status is refused rather than applied.

**Payment files are handed over by temporary file and rename.** The file is written as `.tmp` and
renamed into place, so the bank does not read a half-written payment. The name is
`payment_<internal-id>.xml` from the database row id, never the client's key — the schema
constrains that key only by length, so `../../../a` is schema-valid, and `WriteFileAtomic` also
refuses any name resolving outside the bank folder. The key still travels inside the file as
`MsgId`, which is what the bank echoes in its response and therefore what correlates the two.

**Validation runs against the supplied schema file.** `resources/request_schema.json` is embedded
with `go:embed` and compiled by `santhosh-tekuri/jsonschema`, so the contract we validate against
is the file we were given rather than hand-written checks that can drift from it — and no Gin
binding tags duplicating the same rules. The generated XML likewise follows the provided
`payment.xsd` verbatim, including its quirk of naming the debtor's account `CdtrAcct`.

**Bank responses are polled with a stability delay.** A ticker globs the adapter's response
pattern and skips any file whose mtime is newer than one poll interval, which covers the ordinary
case of a file still being written. Polling needs no extra dependency and no inotify semantics to
reason about. A parsed file moves to `processed/`; anything with a row we could not apply moves to
`failed/`, so an unresolved response stays visible instead of looking handled.

## Tests

```bash
make test    # go test ./...
```

Hand-written fakes rather than a mocking framework: the interfaces are small, so a fake is
shorter than the generated code and needs no toolchain to be installed. Tests live in
external `_test` packages, so they only see each package's public surface.

## Trade-offs and what I would do next

- **The creation path spans two resources.** SQLite and the bank folder cannot join one local
  transaction. The service persists the payment before depositing the file, so a file is not
  produced without local payment state, and a deposit that fails is recorded as `FAILED`. A crash
  between the two steps can still leave a `PENDING` row whose file was never written; production
  would model the deposit lifecycle separately and reconcile ambiguous submissions.
- **The poller assumes one process owns the bank folder.** Two replicas polling the same directory
  would race for the same response file. Multiple instances would need explicit work ownership.
- **File completion is a heuristic.** The stability delay is sufficient here, but a bank that
  stalls mid-write for longer than the interval would still be read early. A real integration
  would agree an explicit completed-file protocol, such as writing to a temporary name and
  renaming it when done — which is what `cmd/fakebank` does.
- **Observability stops at logs.** Structured logs via `log/slog` cover the flow; production would
  add metrics and alerts on deposit failures, poller lag and status transitions.
- **SQLite suits the exercise, not the volume.** The write path is serialised to one connection.
  A multi-instance deployment would want a database built for that concurrency model; the
  `Repository` interface is where that swap happens.

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
