package fab

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Se dispara por lo que tocó el diff: pantallas, plantillas, PDFs y mails; los tests no cuentan.
func TestEsVisible(t *testing.T) {
	for ruta, quiero := range map[string]bool{
		"production.html":                         true,
		"features/tickets/components/Tablero.tsx": true,
		"js/ficha-produccion.js":                  false,
		"js/pdf-produccion.js":                    true,
		"internal/tickets/smtp/plantilla.go":      true,
		"internal/tickets/smtp/resuelto.go":       false,
		"app/globals.css":                         true,
		"test/pdf-produccion.aceptacion.test.js":  false,
		"features/x/Tablero.test.tsx":             false,
		"internal/mail/mail_test.go":              false,
		"internal/tickets/tickets.go":             false,
	} {
		if got := esVisible(ruta); got != quiero {
			t.Errorf("esVisible(%q) = %v, quiero %v", ruta, got, quiero)
		}
	}
}

// Solo corre si la vuelta venía pasando: un revisor caído (que no es del entorno) o un HIGH la frenan.
func TestVieneLimpia(t *testing.T) {
	ok := Review{Revisor: "codigo", Veredicto: "ok"}
	casos := []struct {
		nombre string
		rs     []Review
		quiero bool
	}{
		{"todo ok", []Review{ok, {Revisor: "seguridad", Hallazgos: []Hallazgo{{Severidad: "MEDIUM"}}}}, true},
		{"un HIGH", []Review{ok, {Revisor: "codigo", Hallazgos: []Hallazgo{{Severidad: "HIGH"}}}}, false},
		{"un revisor caído", []Review{ok, {Revisor: "seguridad", Err: "no entregó"}}, false},
		{"UX sin levantar la app no frena", []Review{ok, {Revisor: "ux", Err: infraPrefijo + "no sé levantar acme"}}, true},
	}
	for _, c := range casos {
		if got := vieneLimpia(c.rs); got != c.quiero {
			t.Errorf("%s: %v, quiero %v", c.nombre, got, c.quiero)
		}
	}
}

// Lo que el revisor escriba o commitee en el worktree no llega a la aprobación.
func TestRestaurarDejaElCommitRevisado(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	corre := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	escribir := func(nombre, txt string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, nombre), []byte(txt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	corre("init", "-q")
	escribir(".gitignore", "node_modules/\n")
	escribir("app.js", "uno\n")
	corre("add", ".")
	corre("commit", "-qm", "revisado")
	revisado, _ := git(ctx, dir, "rev-parse", "HEAD")

	escribir("app.js", "tocado\n")
	if err := os.MkdirAll(filepath.Join(dir, ".resultado"), 0o755); err != nil {
		t.Fatal(err)
	}
	escribir(".resultado/salida.pdf", "pdf")
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	escribir("node_modules/dep.js", "dep")
	escribir("extra.js", "x\n")
	corre("add", "extra.js")
	corre("commit", "-qm", "del revisor")

	runs := t.TempDir()
	tk, err := AddTask(runs, "x", "prompt", EnCurso, "acme")
	if err != nil {
		t.Fatal(err)
	}
	rama, _ := git(ctx, dir, "symbolic-ref", "--short", "HEAD")
	corre("checkout", "-q", "-b", "otra-del-revisor")
	restaurar(ctx, tk, Repo{Name: "acme", WT: dir}, rama, revisado)
	if actual, _ := git(ctx, dir, "symbolic-ref", "--short", "HEAD"); actual != rama {
		t.Fatalf("rama = %s, quiero %s", actual, rama)
	}

	if head, _ := git(ctx, dir, "rev-parse", "HEAD"); head != revisado {
		t.Fatalf("HEAD = %s, quiero el revisado %s", head, revisado)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "app.js")); string(b) != "uno\n" {
		t.Fatalf("app.js = %q", b)
	}
	for _, f := range []string{".resultado/salida.pdf", "extra.js"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s tendría que haberse borrado", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "dep.js")); err != nil {
		t.Error("lo ignorado (node_modules) se queda")
	}
}
