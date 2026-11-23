package fab

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repoDePrueba(t *testing.T) (clone, wt, sha string) {
	t.Helper()
	dir := t.TempDir()
	clone, wt = filepath.Join(dir, "clon"), filepath.Join(dir, "wt")
	run := func(args ...string) {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main", clone)
	run("-C", clone, "commit", "-q", "--allow-empty", "-m", "base")
	run("-C", clone, "worktree", "add", "-q", "-b", "factory/x", wt)
	run("-C", wt, "commit", "-q", "--allow-empty", "-m", "cambio")
	sha, _ = git(t.Context(), wt, "rev-parse", "HEAD")
	return clone, wt, sha
}

// La aprobación sube solo lo aprobado: ni un commit después, ni cambios sin commitear.
func TestVerificarAprobado(t *testing.T) {
	clone, wt, sha := repoDePrueba(t)
	if err := verificarAprobado(t.Context(), clone, wt, "factory/x", sha); err != nil {
		t.Fatalf("lo aprobado tal cual tiene que pasar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, "suelto.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verificarAprobado(t.Context(), clone, wt, "factory/x", sha); err == nil {
		t.Fatal("con un archivo sin commitear no se sube")
	}
	_ = os.Remove(filepath.Join(wt, "suelto.txt"))
	if out, err := exec.Command("git", "-C", wt, "commit", "-q", "--allow-empty", "-m", "después").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if err := verificarAprobado(t.Context(), clone, wt, "factory/x", sha); err == nil {
		t.Fatal("un commit después de la revisión no se sube")
	}
	if err := verificarAprobado(t.Context(), clone, wt, "factory/x", ""); err == nil {
		t.Fatal("sin aprobado no se sube")
	}
}

func TestSucios(t *testing.T) {
	_, wt, _ := repoDePrueba(t)
	if s, err := Sucios(t.Context(), Repo{WT: wt}); err != nil || len(s) != 0 {
		t.Fatalf("limpio: %v %v", s, err)
	}
	_ = os.WriteFile(filepath.Join(wt, "a.go"), []byte("x"), 0o644)
	if s, _ := Sucios(t.Context(), Repo{WT: wt}); len(s) != 1 {
		t.Fatalf("quería 1 sucio: %v", s)
	}
}
