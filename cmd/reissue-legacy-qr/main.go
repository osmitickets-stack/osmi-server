package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmitickets-stack/osmi-server/internal/maintenance"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

func main() {
	apply := flag.Bool("apply", false, "store a signed QR for the one eligible legacy ticket")
	expectedDB := flag.String("expected-database", "", "required exact database name")
	flag.Parse()
	if *expectedDB == "" || (*apply && os.Getenv("OSMI_REISSUE_ONE_LEGACY_QR") != "YES") {
		fatal("expected-database is required; apply also requires OSMI_REISSUE_ONE_LEGACY_QR=YES")
	}

	url := os.Getenv("DATABASE_URL")
	keyID := os.Getenv("TICKET_QR_ACTIVE_KEY_ID")
	key := os.Getenv("TICKET_QR_SIGNING_KEY")
	if url == "" || keyID == "" || key == "" {
		fatal("database URL and current QR signing configuration are required")
	}
	signer, err := security.NewTicketCredentialService(keyID, []byte(key))
	if err != nil {
		fatal("invalid QR signing configuration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fatal("database connection failed")
	}
	defer pool.Close()
	count, err := maintenance.ReissueLegacyQR(ctx, pool, signer, *expectedDB, *apply)
	if err != nil {
		if errors.Is(err, maintenance.ErrUnsafeLegacyTicketSelection) {
			fatal(fmt.Sprintf("refused: eligible ticket count is %d; expected exactly one", count))
		}
		fatal("reissue failed; database identity, payment or ticket state did not pass verification")
	}
	if *apply {
		fmt.Println("ONE_LEGACY_QR_SIGNED")
	} else {
		fmt.Println("ONE_LEGACY_TICKET_ELIGIBLE; NO_CHANGES")
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
