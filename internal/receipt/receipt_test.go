package receipt

import (
	"errors"
	"strings"
	"testing"
)

func TestExtractSingle(t *testing.T) {
	raw := "He terminado la tarea.\n\n```weaver-receipt\n" +
		`{"task_id":"T1","status":"done","summary":"Implementé auth","spec_refs":["REQ-1"],"changes":[{"path":"auth.go","kind":"added"}],"commits":["abc1234"],"verification":[{"cmd":"go test ./...","exit_code":0,"output_tail":"PASS"}],"open_issues":[]}` +
		"\n```\n"

	r, err := Extract(raw)
	if err != nil {
		t.Fatalf("Extract() error: %v", err)
	}
	if r.TaskID != "T1" || r.Status != "done" || r.Summary != "Implementé auth" {
		t.Fatalf("unexpected receipt: %+v", r)
	}
	if len(r.Changes) != 1 || r.Changes[0].Path != "auth.go" || r.Changes[0].Kind != "added" {
		t.Fatalf("unexpected changes: %+v", r.Changes)
	}
	if len(r.Verification) != 1 || r.Verification[0].Cmd != "go test ./..." || r.Verification[0].ExitCode != 0 {
		t.Fatalf("unexpected verification: %+v", r.Verification)
	}
}

func TestExtractLastValidWins(t *testing.T) {
	t.Run("template then real", func(t *testing.T) {
		raw := "Plantilla:\n```weaver-receipt\n" +
			`{"task_id":"T1","status":"done | blocked | failed","summary":"ejemplo"}` +
			"\n```\nReal:\n```weaver-receipt\n" +
			`{"task_id":"T1","status":"done","summary":"Listo de verdad","changes":[],"commits":[],"verification":[]}` +
			"\n```\n"

		r, err := Extract(raw)
		if err != nil {
			t.Fatalf("Extract() error: %v", err)
		}
		if err := r.Validate("T1"); err != nil {
			t.Fatalf("Validate() error: %v", err)
		}
		if r.Summary != "Listo de verdad" {
			t.Fatalf("Summary = %q, want %q", r.Summary, "Listo de verdad")
		}
	})

	t.Run("valid then broken json", func(t *testing.T) {
		raw := "Primero válido:\n```weaver-receipt\n" +
			`{"task_id":"T2","status":"blocked","summary":"Falta dep"}` +
			"\n```\nÚltimo roto:\n```weaver-receipt\n" +
			`{"task_id": "T2", broken json` +
			"\n```\n"

		r, err := Extract(raw)
		if err != nil {
			t.Fatalf("Extract() error: %v", err)
		}
		if r.TaskID != "T2" || r.Status != "blocked" {
			t.Fatalf("got %+v, want earlier valid receipt", r)
		}
	})
}

func TestExtractNone(t *testing.T) {
	_, err := Extract("No hay ningún bloque aquí.\n```json\n{}\n```\n")
	if !errors.Is(err, ErrNoReceipt) {
		t.Fatalf("Extract() error = %v, want ErrNoReceipt", err)
	}
}

func TestExtractCRLF(t *testing.T) {
	raw := "Texto\r\n```weaver-receipt\r\n" +
		"{\"task_id\":\"T1\",\"status\":\"done\",\"summary\":\"Con CRLF\"}\r\n" +
		"```\r\nFin\r\n"

	r, err := Extract(raw)
	if err != nil {
		t.Fatalf("Extract() error: %v", err)
	}
	if r.TaskID != "T1" || r.Summary != "Con CRLF" {
		t.Fatalf("got %+v", r)
	}
}

func TestValidate(t *testing.T) {
	validBase := func() Receipt {
		return Receipt{
			TaskID:  "T1",
			Status:  "done",
			Summary: "Cambios realizados",
			Changes: []Change{{Path: "a.go", Kind: "modified"}},
			Verification: []Check{
				{Cmd: "go test ./...", ExitCode: 0, OutputTail: "ok"},
			},
		}
	}

	words151 := strings.TrimSpace(strings.Repeat("palabra ", 151))

	tests := []struct {
		name    string
		mutate  func(*Receipt)
		wantErr string
	}{
		{
			name:    "valid receipt",
			mutate:  func(r *Receipt) {},
			wantErr: "",
		},
		{
			name:    "wrong task_id",
			mutate:  func(r *Receipt) { r.TaskID = "T2" },
			wantErr: "task_id",
		},
		{
			name:    "invalid status",
			mutate:  func(r *Receipt) { r.Status = "done | blocked | failed" },
			wantErr: "status",
		},
		{
			name:    "empty summary",
			mutate:  func(r *Receipt) { r.Summary = "   " },
			wantErr: "summary",
		},
		{
			name:    "summary exceeds 150 words",
			mutate:  func(r *Receipt) { r.Summary = words151 },
			wantErr: "150",
		},
		{
			name:    "empty verification cmd",
			mutate:  func(r *Receipt) { r.Verification = []Check{{Cmd: "  ", ExitCode: 0}} },
			wantErr: "cmd",
		},
		{
			name:    "invalid change kind",
			mutate:  func(r *Receipt) { r.Changes = []Change{{Path: "a.go", Kind: "renamed"}} },
			wantErr: "kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validBase()
			tt.mutate(&r)
			err := r.Validate("T1")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
