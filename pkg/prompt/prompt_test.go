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

func TestApplyPromotionRequest(t *testing.T) {
	t.Run("rejects non-personal prompts", func(t *testing.T) {
		p := &Prompt{Scope: ScopeGlobal}
		if err := p.ApplyPromotionRequest(ScopePersona, []string{"analyst"}); err == nil {
			t.Error("expected error promoting a non-personal prompt")
		}
	})

	t.Run("rejects invalid target scope", func(t *testing.T) {
		p := &Prompt{Scope: ScopePersonal}
		if err := p.ApplyPromotionRequest("bogus", nil); err == nil {
			t.Error("expected error for invalid requested scope")
		}
		if err := p.ApplyPromotionRequest(ScopePersonal, nil); err == nil {
			t.Error("requesting personal scope should be rejected")
		}
	})

	t.Run("persona request requires personas", func(t *testing.T) {
		p := &Prompt{Scope: ScopePersonal}
		if err := p.ApplyPromotionRequest(ScopePersona, nil); err == nil {
			t.Error("persona promotion without personas should be rejected")
		}
	})

	t.Run("records a persona request", func(t *testing.T) {
		p := &Prompt{Scope: ScopePersonal}
		if err := p.ApplyPromotionRequest(ScopePersona, []string{"analyst"}); err != nil {
			t.Fatal(err)
		}
		if !p.ReviewRequested || p.RequestedScope != ScopePersona || len(p.RequestedPersonas) != 1 {
			t.Errorf("request not recorded: %+v", p)
		}
		if p.Scope != ScopePersonal {
			t.Error("scope must not change on request")
		}
	})

	t.Run("global request clears personas", func(t *testing.T) {
		p := &Prompt{Scope: ScopePersonal}
		if err := p.ApplyPromotionRequest(ScopeGlobal, []string{"ignored"}); err != nil {
			t.Fatal(err)
		}
		if p.RequestedScope != ScopeGlobal || len(p.RequestedPersonas) != 0 {
			t.Errorf("global request should not carry personas: %+v", p)
		}
	})
}

func TestApprovePromotion(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	t.Run("rejects when no request pending", func(t *testing.T) {
		p := &Prompt{Scope: ScopePersonal}
		if err := p.ApprovePromotion("admin@x", now); err == nil {
			t.Error("approve with no pending request should error")
		}
	})

	t.Run("applies persona promotion", func(t *testing.T) {
		p := &Prompt{
			Scope: ScopePersonal, Status: StatusDraft, OwnerEmail: "u@x",
			ReviewRequested: true, RequestedScope: ScopePersona, RequestedPersonas: []string{"analyst", "engineer"},
		}
		if err := p.ApprovePromotion("admin@x", now); err != nil {
			t.Fatal(err)
		}
		if p.Scope != ScopePersona || len(p.Personas) != 2 {
			t.Errorf("scope/personas not applied: %+v", p)
		}
		if p.Status != StatusApproved || p.ApprovedBy != "admin@x" || p.ApprovedAt == nil {
			t.Errorf("approval not stamped: %+v", p)
		}
		if p.ReviewRequested || p.RequestedScope != "" || len(p.RequestedPersonas) != 0 {
			t.Errorf("request not cleared: %+v", p)
		}
	})

	t.Run("global promotion clears personas", func(t *testing.T) {
		p := &Prompt{
			Scope: ScopePersonal, Status: StatusDraft, Personas: []string{"stale"},
			ReviewRequested: true, RequestedScope: ScopeGlobal,
		}
		if err := p.ApprovePromotion("admin@x", now); err != nil {
			t.Fatal(err)
		}
		if p.Scope != ScopeGlobal || len(p.Personas) != 0 {
			t.Errorf("global promotion should clear personas: %+v", p)
		}
	})
}

func TestRejectPromotion(t *testing.T) {
	p := &Prompt{
		Scope: ScopePersonal, ReviewRequested: true,
		RequestedScope: ScopePersona, RequestedPersonas: []string{"analyst"},
	}
	p.RejectPromotion()
	if p.ReviewRequested || p.RequestedScope != "" || len(p.RequestedPersonas) != 0 {
		t.Errorf("request not cleared: %+v", p)
	}
	if p.Scope != ScopePersonal {
		t.Error("reject must leave scope personal")
	}
}

func TestApplyPromotionRequest_RejectsTerminalStatus(t *testing.T) {
	for _, s := range []string{StatusDeprecated, StatusSuperseded} {
		p := &Prompt{Scope: ScopePersonal, Status: s}
		if err := p.ApplyPromotionRequest(ScopeGlobal, nil); err == nil {
			t.Errorf("promotion request on %s prompt should be rejected", s)
		}
	}
}

func TestApprovePromotion_StaleWhenNoLongerPersonal(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	p := &Prompt{
		Scope:           ScopeGlobal, // scope changed out from under the request
		ReviewRequested: true, RequestedScope: ScopePersona, RequestedPersonas: []string{"analyst"},
	}
	if err := p.ApprovePromotion("admin@x", now); err == nil {
		t.Fatal("expected stale-request error when prompt is no longer personal")
	}
	if p.Scope != ScopeGlobal {
		t.Errorf("scope must not be re-stamped: %q", p.Scope)
	}
	if p.ReviewRequested {
		t.Error("stale request should be cleared")
	}
}

func TestApprovePromotion_ClearsLifecycleMarkers(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	dep := now
	p := &Prompt{
		Scope: ScopePersonal, Status: StatusApproved,
		DeprecatedAt: &dep, SupersededBy: "old-v1",
		ReviewRequested: true, RequestedScope: ScopeGlobal,
	}
	if err := p.ApprovePromotion("admin@x", now); err != nil {
		t.Fatal(err)
	}
	if p.DeprecatedAt != nil || p.SupersededBy != "" {
		t.Errorf("stale lifecycle markers not cleared: %+v", p)
	}
}
