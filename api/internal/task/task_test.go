package task

import (
	"errors"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// fieldErrors returns the field map of a *ValidationError, or nil if err is nil.
func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	if err == nil {
		return nil
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	return verr.Fields
}

func TestCreateInputNormalize(t *testing.T) {
	in := CreateInput{Title: "  write ADR  "}
	in.Normalize()
	if in.Title != "write ADR" {
		t.Errorf("title = %q, want trimmed", in.Title)
	}
	if in.Status != StatusTodo {
		t.Errorf("status = %q, want default %q", in.Status, StatusTodo)
	}
}

func TestCreateInputValidate(t *testing.T) {
	cases := []struct {
		name       string
		in         CreateInput
		wantFields []string
	}{
		{"valid minimal", CreateInput{Title: "a", Status: StatusTodo}, nil},
		{"valid max lengths, multibyte", CreateInput{Title: strings.Repeat("ñ", 200), Description: strings.Repeat("é", 2000), Status: StatusDone}, nil},
		{"empty title", CreateInput{Title: "", Status: StatusTodo}, []string{"title"}},
		{"title too long", CreateInput{Title: strings.Repeat("a", 201), Status: StatusTodo}, []string{"title"}},
		{"description too long", CreateInput{Title: "a", Description: strings.Repeat("a", 2001), Status: StatusTodo}, []string{"description"}},
		{"bad status", CreateInput{Title: "a", Status: "blocked"}, []string{"status"}},
		{"all wrong at once", CreateInput{Title: "", Description: strings.Repeat("a", 2001), Status: "x"}, []string{"title", "description", "status"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fieldErrors(t, tc.in.Validate())
			if len(got) != len(tc.wantFields) {
				t.Fatalf("field errors = %v, want fields %v", got, tc.wantFields)
			}
			for _, f := range tc.wantFields {
				if _, ok := got[f]; !ok {
					t.Errorf("missing error for field %q in %v", f, got)
				}
			}
		})
	}
}

func TestUpdateInputValidate(t *testing.T) {
	cases := []struct {
		name       string
		in         UpdateInput
		wantFields []string
	}{
		{"status only", UpdateInput{Status: ptr(StatusDone)}, nil},
		{"empty description allowed", UpdateInput{Description: ptr("")}, nil},
		{"no fields", UpdateInput{}, []string{"body"}},
		{"empty title", UpdateInput{Title: ptr("")}, []string{"title"}},
		{"bad status", UpdateInput{Status: ptr(Status("blocked"))}, []string{"status"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fieldErrors(t, tc.in.Validate())
			if len(got) != len(tc.wantFields) {
				t.Fatalf("field errors = %v, want fields %v", got, tc.wantFields)
			}
			for _, f := range tc.wantFields {
				if _, ok := got[f]; !ok {
					t.Errorf("missing error for field %q in %v", f, got)
				}
			}
		})
	}
}

func TestUpdateInputNormalizeTrimsTitle(t *testing.T) {
	in := UpdateInput{Title: ptr("   ")}
	in.Normalize()
	if err := fieldErrors(t, in.Validate()); err["title"] == "" {
		t.Errorf("whitespace-only title should be rejected after Normalize, got %v", err)
	}
}

func TestValidID(t *testing.T) {
	cases := map[string]bool{
		"01a17e64-8852-7c2f-ae4c-1258cb92a5cb":  true,
		"01A17E64-8852-7C2F-AE4C-1258CB92A5CB":  true,
		"01a17e6488527c2fae4c1258cb92a5cb":      false, // no hyphens
		"01a17e64-8852-7c2f-ae4c-1258cb92a5c":   false, // too short
		"01a17e64-8852-7c2f-ae4c-1258cb92a5cg":  false, // non-hex
		"01a17e64_8852-7c2f-ae4c-1258cb92a5cb":  false, // wrong separator
		"":                                      false,
		"'; DROP TABLE tasks; --xxxxxxxxxxxxxx": false,
	}
	for id, want := range cases {
		if got := ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestValidationErrorMessageIsDeterministic(t *testing.T) {
	err := (&CreateInput{Title: "", Status: "x"}).Validate()
	want := "validation failed: status: must be one of todo, in_progress, done; title: is required"
	if err == nil || err.Error() != want {
		t.Errorf("Error() = %v, want %q", err, want)
	}
}
