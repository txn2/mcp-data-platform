package resource

import (
	"testing"
)

// The subject a run's author authenticates as keys the library the run files in
// (#1677). These pin the three rules that read it -- the default library, the
// visible libraries, and "is this scope mine" -- and the two ways it is absent:
// a human caller, who has no second identity, and a run whose author the
// platform has not seen authenticate, which stays keyed by address.

func runWithSubject() Claims {
	return BuildClaims("script:daily", "owner@example.com", "analyst", []string{"analyst"}, false).
		ActingFor("author@example.com", "author-sub")
}

func runWithoutSubject() Claims {
	return BuildClaims("script:daily", "owner@example.com", "analyst", []string{"analyst"}, false).
		ActingFor("author@example.com", "")
}

func TestActingFor_ReadsTheSubjectOnlyBesideAnAddress(t *testing.T) {
	c := BuildClaims("sub-1", "me@example.com", "", nil, false).ActingFor("", "stray-sub")
	if c.OnBehalfOf != "" || c.OnBehalfOfSub != "" {
		t.Fatalf("a subject with no address must name nobody: %+v", c)
	}
	if got := PersonSubject(c); got != "sub-1" {
		t.Fatalf("PersonSubject = %q, want the caller's own", got)
	}
}

func TestPersonSubject(t *testing.T) {
	tests := []struct {
		name string
		c    Claims
		want string
	}{
		{"a person is their own subject", BuildClaims("sub-1", "me@example.com", "", nil, false), "sub-1"},
		{"a run acting for a known author is the author's subject", runWithSubject(), "author-sub"},
		{"a run acting for an unseen author is its principal", runWithoutSubject(), "script:daily"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PersonSubject(tc.c); got != tc.want {
				t.Fatalf("PersonSubject = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveScopeFor_ARunFilesWhereItsAuthorsSessionDoes(t *testing.T) {
	tests := []struct {
		name string
		c    Claims
		want string
	}{
		{"a session files by its subject", BuildClaims("sub-1", "me@example.com", "", nil, false), "sub-1"},
		{"a run of a seen author files by the author's subject", runWithSubject(), "author-sub"},
		{"a run of an unseen author files by the author's address", runWithoutSubject(), "author@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope, id := ResolveScopeFor("", "", tc.c)
			if scope != ScopeUser || id != tc.want {
				t.Fatalf("ResolveScopeFor = %s/%q, want user/%q", scope, id, tc.want)
			}
		})
	}
	t.Run("a named library is left alone", func(t *testing.T) {
		_, id := ResolveScopeFor(ScopeUser, "somebody-else", runWithSubject())
		if id != "somebody-else" {
			t.Fatalf("scope id = %q", id)
		}
	})
}

func TestVisibleScopes_ARunSeesItsAuthorsSubjectLibrary(t *testing.T) {
	has := func(scopes []ScopeFilter, id string) bool {
		for _, s := range scopes {
			if s.Scope == ScopeUser && s.ScopeID == id {
				return true
			}
		}
		return false
	}
	t.Run("seen author: subject, address and principal", func(t *testing.T) {
		scopes := VisibleScopes(runWithSubject())
		for _, id := range []string{"author-sub", "author@example.com", "script:daily"} {
			if !has(scopes, id) {
				t.Errorf("library %q is not visible: %+v", id, scopes)
			}
		}
		if has(scopes, "owner@example.com") {
			t.Errorf("the owner's address is not the run's library: %+v", scopes)
		}
	})
	t.Run("unseen author: address and principal only", func(t *testing.T) {
		scopes := VisibleScopes(runWithoutSubject())
		if !has(scopes, "author@example.com") || !has(scopes, "script:daily") {
			t.Errorf("visible = %+v", scopes)
		}
		if len(scopes) != 4 { // global + principal + address + persona
			t.Errorf("visible = %+v", scopes)
		}
	})
	t.Run("a person whose subject equals their address lists it once", func(t *testing.T) {
		scopes := VisibleScopes(BuildClaims("me@example.com", "me@example.com", "", nil, false))
		n := 0
		for _, s := range scopes {
			if s.Scope == ScopeUser {
				n++
			}
		}
		if n != 1 {
			t.Errorf("user libraries listed %d times: %+v", n, scopes)
		}
	})
}

func TestCanWriteScope_ARunWritesItsAuthorsSubjectLibrary(t *testing.T) {
	c := runWithSubject()
	if !CanWriteScope(c, ScopeUser, "author-sub") {
		t.Error("the author's subject library is the run's own")
	}
	if !CanWriteScope(c, ScopeUser, "author@example.com") {
		t.Error("the author's address library is still the run's own")
	}
	if CanWriteScope(c, ScopeUser, "owner@example.com") {
		t.Error("the owner's library is not the run's")
	}
	if CanWriteScope(runWithoutSubject(), ScopeUser, "author-sub") {
		t.Error("a subject the platform has not recorded grants nothing")
	}
}
