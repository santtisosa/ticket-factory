package fab

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// El modelo y el effort del frontmatter llegan a la llamada como flags explícitos.
func TestModeloPorAgente(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "ux.md")
	if err := os.WriteFile(md, []byte("---\nname: ux\ndescription: d\nmodel: sonnet\neffort: medium\n---\nprompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "agents.json")
	if err := LoadAgents(map[string]string{"ux": md}, out); err != nil {
		t.Fatal(err)
	}
	m := ModeloDe(out, "ux")
	if m.Model != "sonnet" || m.Effort != "medium" {
		t.Fatalf("modelo: %+v", m)
	}
	if b, _ := os.ReadFile(out); strings.Contains(string(b), "effort") {
		t.Fatal("effort no va en el JSON de --agents (no está documentado ahí)")
	}
	if ModeloDe(out, "otro") != (Modelo{}) {
		t.Fatal("un agente sin modelo usa el de la cuenta")
	}
	a := Call{Modelo: m, Schema: []byte("{}")}.args()
	if i := slices.Index(a, "--model"); i < 0 || a[i+1] != "sonnet" {
		t.Fatalf("--model: %v", a)
	}
	if i := slices.Index(a, "--effort"); i < 0 || a[i+1] != "medium" {
		t.Fatalf("--effort: %v", a)
	}
	if slices.Contains(Call{Schema: []byte("{}")}.args(), "--model") {
		t.Fatal("sin modelo no se pasa --model")
	}
}

// Cortar a un experto le da la chance de cerrar el turno (SIGINT) antes de matarlo.
func TestCorteSuave(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `trap 'echo cerre-el-turno; exit 0' INT; sleep 30 & wait`)
	enGrupoSuave(cmd)
	inicio := time.Now()
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "cerre-el-turno") {
		t.Fatalf("con SIGINT el experto entrega su salida: %q", out)
	}
	if time.Since(inicio) > 5*time.Second {
		t.Fatal("no espera la gracia entera si sale con el SIGINT")
	}
}

// Sesión del Builder: la vuelta 1 la abre con --session-id, la 2 la retoma con --resume; sin sesión,
// no se guarda nada. El costo de una sesión retomada es acumulado: se cuenta solo lo nuevo.
func TestSesionDelBuilder(t *testing.T) {
	base := Call{Schema: []byte("{}")}
	if a := base.args(); !slices.Contains(a, "--no-session-persistence") || slices.Contains(a, "--resume") {
		t.Fatalf("sin sesión: %v", a)
	}
	nueva := base
	nueva.SessionID = nuevoID()
	a := nueva.args()
	if i := slices.Index(a, "--session-id"); i < 0 || a[i+1] != nueva.SessionID || slices.Contains(a, "--no-session-persistence") {
		t.Fatalf("sesión nueva: %v", a)
	}
	retoma := nueva
	retoma.Resume = true
	a = retoma.args()
	if i := slices.Index(a, "--resume"); i < 0 || a[i+1] != nueva.SessionID || slices.Contains(a, "--session-id") {
		t.Fatalf("retomar: %v", a)
	}
	if len(nueva.SessionID) != 36 || nueva.SessionID[14] != '4' {
		t.Fatalf("UUID v4: %q", nueva.SessionID)
	}
	if nuevoID() == nueva.SessionID {
		t.Fatal("cada sesión, un id")
	}
	// 0,265 de la vuelta 1 + 0,009 de la 2 = 0,274 informado: lo propio de la 2 es 0,009.
	if got := (Result{CostUSD: 0.274}).CostoPropio(0.265); got < 0.0089 || got > 0.0091 {
		t.Fatalf("costo propio: %v", got)
	}
	if (Result{CostUSD: 0.1}).CostoPropio(0.2) != 0 {
		t.Fatal("nunca negativo")
	}
	task := &Task{Dir: t.TempDir()}
	if _, ok := leerSesion(task, "03-builder"); ok {
		t.Fatal("sin sesión guardada")
	}
	guardarSesion(task, "03-builder", sesion{ID: nueva.SessionID, Costo: 0.265})
	if s, ok := leerSesion(task, "03-builder"); !ok || s.ID != nueva.SessionID || s.Costo != 0.265 {
		t.Fatalf("sesión guardada: %+v", s)
	}
	if p := promptRetomar(2, "arregla X"); !strings.Contains(p, "arregla X") || strings.Contains(p, "congelados") {
		t.Fatalf("prompt de retomar: %q", p)
	}
}
