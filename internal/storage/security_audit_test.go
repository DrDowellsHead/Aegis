package storage

import (
	"context"
	"testing"
	"time"
)

func TestValidateSecurityEventAndQuery(t *testing.T) {
	good := SecurityEventInput{Action: "login_ok", Actor: "operator", Role: "admin"}
	if err := ValidateSecurityEvent(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SecurityEventInput{
		{Action: "secret", Actor: "operator"},
		{Action: "login_ok", Actor: "bad name"},
		{Action: "login_ok", Actor: "operator", Subject: "some@email"},
		{Action: "login_ok", Actor: "operator", Role: "root"},
		{Action: "sessions_revoked", Actor: "operator", AffectedSessions: 129},
	} {
		if ValidateSecurityEvent(bad) == nil {
			t.Fatalf("invalid audit event accepted: %+v", bad)
		}
	}
	for _, q := range []SecurityEventQuery{
		{Limit: 0}, {Limit: 101}, {Limit: 20, Cursor: "0"}, {Limit: 20, Cursor: "oops"},
		{Limit: 20, Action: "who"}, {Limit: 20, Actor: "bad actor"},
	} {
		if ValidateSecurityQuery(q) == nil {
			t.Fatalf("invalid security query accepted: %+v", q)
		}
	}
}

func TestSecurityEventsIntegratedWithPostgres(t *testing.T) {
	db, ctx, _ := incidentDB(t)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.db.ExecContext(c, "DELETE FROM aegis_security_events WHERE actor=$1", "audit_security_test")
	})
	for _, action := range []string{"login_failed", "login_ok", "logout", "sessions_revoked"} {
		if err := db.RecordSecurityEvent(ctx, SecurityEventInput{
			Action: action, Actor: "audit_security_test", Role: "admin", Subject: "observer",
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := db.ListSecurityEvents(ctx, SecurityEventQuery{Actor: "audit_security_test", Limit: 2})
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page one %+v %v", page, err)
	}
	second, err := db.ListSecurityEvents(ctx, SecurityEventQuery{
		Actor: "audit_security_test", Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(second.Items) != 2 || second.HasMore ||
		page.Items[0].ID == second.Items[0].ID {
		t.Fatalf("page two %+v %v", second, err)
	}
	only, err := db.ListSecurityEvents(ctx, SecurityEventQuery{
		Actor: "audit_security_test", Action: "login_failed", Limit: 100})
	if err != nil || len(only.Items) != 1 || only.Items[0].Action != "login_failed" {
		t.Fatalf("filtering failed %+v %v", only, err)
	}
	if err := db.RecordSecurityEvent(ctx, SecurityEventInput{Actor: "bad actor", Action: "login_ok"}); err == nil {
		t.Fatal("unvalidated actor inserted")
	}
}
