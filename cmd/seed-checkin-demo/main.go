// Emits a guarded, transactional fixture for the isolated check-in database.
// It never connects to a database or loads environment files.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
	"github.com/skip2/go-qrcode"
)

func main() {
	emit := flag.Bool("emit-test-sql", false, "emit SQL for the isolated demo database")
	render := flag.String("render-test-qr-png", "", "create a PNG with a test-only signed credential; refuses to overwrite")
	ticket := flag.String("ticket-id", "", "public UUID of the isolated test ticket")
	flag.Parse()
	if *emit == (*render != "") {
		fmt.Fprintln(os.Stderr, "Choose exactly one test-only operation")
		os.Exit(1)
	}
	signer, err := security.NewTicketCredentialService("test-k1", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		panic(err)
	}
	if *render != "" {
		id, err := uuid.Parse(*ticket)
		if err != nil || id == uuid.Nil || id.String() != *ticket {
			fmt.Fprintln(os.Stderr, "A canonical test-ticket UUID is required")
			os.Exit(1)
		}
		credential, err := signer.Sign(id.String())
		if err != nil {
			fmt.Fprintln(os.Stderr, "Could not sign test QR")
			os.Exit(1)
		}
		png, err := qrcode.Encode(credential, qrcode.Medium, 512)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Could not render test QR")
			os.Exit(1)
		}
		file, err := os.OpenFile(*render, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Could not create new test QR image")
			os.Exit(1)
		}
		defer file.Close()
		if _, err := file.Write(png); err != nil {
			fmt.Fprintln(os.Stderr, "Could not write test QR image")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Test-only QR image created")
		return
	}
	// Public throwaway credentials, exclusively for this isolated fixture.
	hash, err := security.NewPasswordHasher().HashPassword("Osmi-Demo-Only-2026!")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not hash demo password")
		os.Exit(1)
	}
	eventID, ticketID := uuid.NewString(), uuid.NewString()
	credential, err := signer.Sign(ticketID)
	if err != nil {
		panic(err)
	}
	fmt.Printf(`\set ON_ERROR_STOP on
BEGIN;
DO $guard$
BEGIN
 IF current_database() <> 'osmidb_test' OR current_user <> 'osmi' THEN
  RAISE EXCEPTION 'Refusing: wrong database or user';
 END IF;
 IF (SELECT count(*) FROM public.osmi_checkin_test_guard) <> 1 OR
    (SELECT marker FROM public.osmi_checkin_test_guard LIMIT 1) IS DISTINCT FROM 'osmi-checkin-isolated-v1' THEN
  RAISE EXCEPTION 'Refusing: isolated database marker missing';
 END IF;
 IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version=13 AND dirty=false) THEN
  RAISE EXCEPTION 'Refusing: schema must be clean at version 13';
 END IF;
END $guard$;
DO $fixture$
DECLARE actor_id bigint; organizer_id bigint; event_id bigint; type_id bigint;
BEGIN
 PERFORM pg_advisory_xact_lock(20261028, 6);
 IF EXISTS (SELECT 1 FROM auth.users WHERE email='scanner.demo@example.invalid') THEN
  RAISE EXCEPTION 'Demo account already exists; no existing data was changed';
 END IF;
 INSERT INTO auth.users(email,password_hash,is_active,is_staff,is_superuser)
 VALUES('scanner.demo@example.invalid','%s',true,false,false) RETURNING id INTO actor_id;
 INSERT INTO ticketing.organizers(name,slug,contact_email)
 VALUES('OSMI isolated scanner demo','scanner-demo-%s','organizer.demo@example.invalid') RETURNING id INTO organizer_id;
 INSERT INTO ticketing.events(public_uuid,organizer_id,slug,name,starts_at,ends_at)
 VALUES('%s',organizer_id,'scanner-demo-%s','OSMI scanner — isolated demo',NOW()-INTERVAL '1 hour',NOW()+INTERVAL '24 hours') RETURNING id INTO event_id;
 INSERT INTO ticketing.event_staff(event_id,user_id,role,is_active) VALUES(event_id,actor_id,'checkin_staff',true);
 INSERT INTO ticketing.ticket_types(event_id,name,base_price,currency,total_quantity,reserved_quantity,sold_quantity)
 VALUES(event_id,'Isolated demo ticket',10,'MXN',1,0,1) RETURNING id INTO type_id;
 INSERT INTO ticketing.tickets(public_uuid,ticket_type_id,event_id,code,secret_hash,qr_code_data,status,final_price,currency,sold_at,validation_count)
 VALUES('%s',type_id,event_id,'DEMO-%s','isolated-demo-placeholder','%s','sold',10,'MXN',NOW(),0);
END $fixture$;
COMMIT;
SELECT 'scanner.demo@example.invalid' AS demo_login, '%s' AS demo_event_id, '%s' AS demo_credential;
`, hash, eventID, eventID, eventID, ticketID, ticketID, credential, eventID, credential)
}
