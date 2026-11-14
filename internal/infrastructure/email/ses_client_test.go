package email

import "testing"

func TestSESClientValidateSenderAddresses(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		replyTo string
		wantErr bool
	}{
		{name: "verified domain address", from: "boletos@myosmi.com", replyTo: "soporte@example.com"},
		{name: "optional reply to", from: "boletos@myosmi.com"},
		{name: "display name rejected", from: "OSMI <boletos@myosmi.com>", wantErr: true},
		{name: "invalid reply to rejected", from: "boletos@myosmi.com", replyTo: "not-an-address", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &SESClient{host: "smtp.example.com", port: 587, username: "test", password: "test", from: tt.from, replyTo: tt.replyTo}
			err := client.validateConfig()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
