package webhook_test

// Header and body redaction logic now lives in internal/gateway/redact.
// The tests for that logic are in internal/gateway/redact/redact_test.go.
//
// This file is intentionally kept to document the migration: the former
// buildRedactedTriggerData function was extracted to redact.Headers /
// redact.Body in §17 P2 M3 and all coverage lives in the shared package.
