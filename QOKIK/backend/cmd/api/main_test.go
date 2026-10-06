package main

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashUsesArgon2AndVerifies(t *testing.T) {
	hash := hashPassword("correct horse battery staple")
	if !verifyPassword("correct horse battery staple", hash) {
		t.Fatal("the password hash did not verify")
	}
	if verifyPassword("wrong password", hash) {
		t.Fatal("an incorrect password verified")
	}
	if verifyPassword("anything", "malformed") {
		t.Fatal("a malformed hash verified")
	}
}

func TestJWTRequiresExpectedUseIssuerAudienceAndAlgorithm(t *testing.T) {
	s := &server{secret: []byte(strings.Repeat("s", 40)), issuer: "issuer", audience: "audience"}
	u := user{ID: 42}
	access, _, err := s.sign(u, "access", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.parse(access, "access"); err != nil {
		t.Fatalf("valid access JWT rejected: %v", err)
	}
	if _, err = s.parse(access, "refresh"); err == nil {
		t.Fatal("access JWT accepted as a refresh JWT")
	}

	wrongAudience := &server{secret: s.secret, issuer: s.issuer, audience: "other"}
	if _, err = wrongAudience.parse(access, "access"); err == nil {
		t.Fatal("JWT with the wrong audience was accepted")
	}

	wrongIssuer := &server{secret: s.secret, issuer: "other", audience: s.audience}
	if _, err = wrongIssuer.parse(access, "access"); err == nil {
		t.Fatal("JWT with the wrong issuer was accepted")
	}
	expired, _, err := s.sign(u, "access", "", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.parse(expired, "access"); err == nil {
		t.Fatal("expired JWT was accepted")
	}
}

func TestRefreshOriginMustBePresentAndAllowlisted(t *testing.T) {
	if allowedOrigin("") {
		t.Fatal("missing Origin must not be accepted")
	}
	if !allowedOrigin("http://127.0.0.1:5174") {
		t.Fatal("the configured E2E origin was rejected")
	}
	if allowedOrigin("https://untrusted.example") {
		t.Fatal("untrusted Origin was accepted")
	}
}
