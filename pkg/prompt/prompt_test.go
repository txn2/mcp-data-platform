package prompt

import (
	"testing"
	"time"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"daily-report", "daily_report", "a", "0x", "report-v2"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v; want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "Daily", "has space", "-leading", "_leading", "bad!"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) = nil; want error", bad)
		}
	}
}

func TestValidateTags(t *testing.T) {
	if err := ValidateTags([]string{"sales", "reporting"}); err != nil {
		t.Errorf("ValidateTags(valid) = %v", err)
	}
	if err := ValidateTags(nil); err != nil {
		t.Errorf("ValidateTags(nil) = %v", err)
	}
	tooMany := make([]string, maxTags+1)
	for i := range tooMany {
		tooMany[i] = "t"
	}
	if ValidateTags(tooMany) == nil {
		t.Error("ValidateTags should reject more than maxTags")
	}
	long := make([]byte, maxTagLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if ValidateTags([]string{string(long)}) == nil {
		t.Error("ValidateTags should reject an over-length tag")
	}
}

func TestValidateScopeAndStatus(t *testing.T) {
	for _, s := range []string{ScopeGlobal, ScopePersona, ScopePersonal} {
		if err := ValidateScope(s); err != nil {
			t.Errorf("ValidateScope(%q) = %v", s, err)
		}
	}
	if ValidateScope("bogus") == nil {
		t.Error("ValidateScope(bogus) should error")
	}
	for _, s := range []string{StatusDraft, StatusApproved, StatusDeprecated, StatusSuperseded} {
		if err := ValidateStatus(s); err != nil {
			t.Errorf("ValidateStatus(%q) = %v", s, err)
		}
	}
	if ValidateStatus("bogus") == nil {
		t.Error("ValidateStatus(bogus) should error")
	}
}

func TestApplyStatusTransition(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	t.Run("no-op when empty or unchanged", func(t *testing.T) {
		p := &Prompt{Status: StatusDraft}
		if err := p.ApplyStatusTransition("", "", "a@x", true, now); err != nil {
			t.Fatal(err)
		}
		if err := p.ApplyStatusTransition(StatusDraft, "", "a@x", true, now); err != nil {
			t.Fatal(err)
		}
		if p.Status != StatusDraft {
			t.Errorf("status changed to %q", p.Status)
		}
	})

	t.Run("rejects unknown status and invalid transition", func(t *testing.T) {
		p := &Prompt{Status: StatusDraft}
		if err := p.ApplyStatusTransition("bogus", "", "a@x", true, now); err == nil {
			t.Error("expected error for unknown status")
		}
		if err := p.ApplyStatusTransition(StatusDeprecated, "", "a@x", true, now); err == nil {
			t.Error("draft->deprecated should be rejected")
		}
	})

	t.Run("approval requires admin and stamps metadata", func(t *testing.T) {
		p := &Prompt{Status: StatusDraft}
		if err := p.ApplyStatusTransition(StatusApproved, "", "u@x", false, now); err == nil {
			t.Error("non-admin approval should be rejected")
		}
		if err := p.ApplyStatusTransition(StatusApproved, "", "admin@x", true, now); err != nil {
			t.Fatal(err)
		}
		if p.Status != StatusApproved || p.ApprovedBy != "admin@x" || p.ApprovedAt == nil {
			t.Errorf("approval metadata not stamped: %+v", p)
		}
	})

	t.Run("supersede records replacement", func(t *testing.T) {
		p := &Prompt{Status: StatusApproved}
		if err := p.ApplyStatusTransition(StatusSuperseded, "report-v2", "admin@x", true, now); err != nil {
			t.Fatal(err)
		}
		if p.Status != StatusSuperseded || p.SupersededBy != "report-v2" {
			t.Errorf("supersede not recorded: %+v", p)
		}
	})
}
