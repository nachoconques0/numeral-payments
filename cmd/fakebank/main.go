// Command fakebank simulates the bank so the flow can be demonstrated: it reads
// payment files and drops a response file a few seconds later.
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

func main() {
	folder := flag.String("bank", os.Getenv("BANK_FOLDER"), "bank folder to watch")
	status := flag.String("status", "PROCESSED", "status to report: PROCESSED or REJECTED")
	delay := flag.Duration("delay", 3*time.Second, "how long the bank takes to answer")
	poll := flag.Duration("poll", time.Second, "how often to look for new payments")
	flag.Parse()

	if *folder == "" {
		log.Fatal("set -bank or BANK_FOLDER")
	}

	log.Printf("fake bank watching %s, answering %s after %s", *folder, *status, *delay)

	answered := map[string]bool{}
	for {
		payments, err := filepath.Glob(filepath.Join(*folder, "payment_*.xml"))
		if err != nil {
			log.Fatalf("glob bank folder: %v", err)
		}

		for _, path := range payments {
			if answered[path] {
				continue
			}
			answered[path] = true

			time.Sleep(*delay)
			if err := answer(*folder, path, *status); err != nil {
				log.Printf("could not answer %s: %v", filepath.Base(path), err)
			}
		}

		time.Sleep(*poll)
	}
}

// answer reads the MsgId out of a payment file and writes the response the
// bank would produce for it.
func answer(folder, path, status string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read payment: %w", err)
	}

	var document struct {
		MessageID string `xml:"GrpHdr>MsgId"`
	}
	if err := xml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("read MsgId: %w", err)
	}

	name := fmt.Sprintf("response_%s.csv", document.MessageID)
	body := fmt.Sprintf("ID, STATUS\n%s, %s\n", document.MessageID, status)

	target := filepath.Join(folder, name)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("move response into place: %w", err)
	}

	log.Printf("answered %s with %s", document.MessageID, status)
	return nil
}
