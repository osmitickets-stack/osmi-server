package security

import "testing"

func TestJWTTokenTypes(t *testing.T) {
	service := NewJWTService("osmi-test-only-jwt-signing-key")
	const userID = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name  string
		issue func(string) (string, error)
		want  string
	}{
		{"access", service.GenerateAccessToken, "access"},
		{"refresh", service.GenerateRefreshToken, "refresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, err := tc.issue(userID)
			if err != nil {
				t.Fatal(err)
			}
			claims, err := service.ValidateToken(token)
			if err != nil {
				t.Fatal(err)
			}
			if claims.UserID != userID || claims.TokenType != tc.want {
				t.Fatalf("wrong token identity or type: user=%q type=%q", claims.UserID, claims.TokenType)
			}
		})
	}
}
