package fab

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// worktreeDeTarea arma <home>/clones/web con un worktree en <home>/runs/t1/wt/web y fija las raíces.
func worktreeDeTarea(t *testing.T) (home string, r Repo) {
	t.Helper()
	home = t.TempDir()
	r = Repo{Name: "web", Src: filepath.Join(home, "clones", "web"), WT: filepath.Join(home, "runs", "t1", "wt", "web")}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main", r.Src)
	if err := os.WriteFile(filepath.Join(r.Src, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-C", r.Src, "add", "a.txt")
	run("-C", r.Src, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-qm", "base")
	run("-C", r.Src, "worktree", "add", "-q", "-b", "factory/t1", r.WT)
	viejos := [2]string{raizClones, raizRuns}
	t.Cleanup(func() { raizClones, raizRuns = viejos[0], viejos[1] })
	raizClones, raizRuns = filepath.Join(home, "clones"), filepath.Join(home, "runs")
	return home, r
}

// gitdirDe: el gitdir real del worktree (git le pone nombre propio).
func gitdirDe(t *testing.T, r Repo) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.WT, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir: "))
}

func TestWorktreeSano(t *testing.T) {
	_, r := worktreeDeTarea(t)
	if err := VerificarWorktrees([]Repo{r}); err != nil {
		t.Fatal(err)
	}
	if sha, err := Head(context.Background(), r); err != nil || len(sha) != 40 {
		t.Fatalf("head: %q %v", sha, err)
	}
}

// Un .git que la rama reescribió para apuntar a otro repo (con su propia config) traba: git no corre.
func TestWorktreeQueApuntaAOtroLadoTraba(t *testing.T) {
	home, r := worktreeDeTarea(t)
	falso := filepath.Join(r.WT, "trampa")
	if out, err := exec.Command("git", "init", "-q", falso).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	cases := map[string]string{
		"otro repo":        "gitdir: " + filepath.Join(falso, ".git"),
		"clon ajeno":       "gitdir: " + filepath.Join(home, "otro", ".git", "worktrees", "web"),
		"sin gitdir":       "basura",
		"otro worktree":    "gitdir: " + filepath.Join(r.Src, ".git", "worktrees", "inventado"),
		"varias líneas":    "gitdir: " + gitdirDe(t, r) + "\ngitdir: /tmp",
		"relativo afuera":  "gitdir: ../../../../trampa/.git",
		"symlink no vale":  "",
		"clon como origen": "gitdir: " + filepath.Join(r.Src, ".git"),
	}
	original, _ := os.ReadFile(filepath.Join(r.WT, ".git"))
	for nombre, contenido := range cases {
		punto := filepath.Join(r.WT, ".git")
		_ = os.Remove(punto)
		if nombre == "symlink no vale" {
			_ = os.WriteFile(filepath.Join(r.WT, "git-real"), original, 0o644)
			_ = os.Symlink(filepath.Join(r.WT, "git-real"), punto)
		} else if err := os.WriteFile(punto, []byte(contenido+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := VerificarWorktrees([]Repo{r}); !errors.Is(err, errWorktreeAjeno) {
			t.Errorf("%s: verificar = %v", nombre, err)
		}
		if _, err := Head(context.Background(), r); !errors.Is(err, errWorktreeAjeno) {
			t.Errorf("%s: git corrió igual: %v", nombre, err)
		}
	}
	// Un directorio bajo runs sin .git (lo borró la rama) tampoco cae al repo de arriba.
	_ = os.Remove(filepath.Join(r.WT, ".git"))
	if _, err := Sucios(context.Background(), r); !errors.Is(err, errWorktreeAjeno) {
		t.Errorf("sin .git: %v", err)
	}
}

// Config que ejecuta comandos (fsmonitor y un filtro clean), plantada donde la rama puede escribir: un
// commondir hacia un repo propio, o config.worktree en el gitdir del worktree. El git de la fábrica no
// la ejecuta. El control sin blindaje sí, así el test prueba algo.
func TestConfigPlantadaNoSeEjecuta(t *testing.T) {
	_, r := worktreeDeTarea(t)
	testigo := filepath.Join(t.TempDir(), "PWNED")
	script := filepath.Join(t.TempDir(), "plantado.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+testigo+"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	planta := "[core]\n\tfsmonitor = " + script + "\n[filter \"x\"]\n\tclean = " + script + "\n"
	gd := gitdirDe(t, r)
	falso := filepath.Join(r.WT, "trampa")
	if out, err := exec.Command("git", "init", "-q", falso).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	_ = os.WriteFile(filepath.Join(falso, ".git", "config"), []byte(planta), 0o644)
	_ = os.WriteFile(filepath.Join(gd, "commondir"), []byte(filepath.Join(falso, ".git")+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(r.WT, ".gitattributes"), []byte("* filter=x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(r.WT, "a.txt"), []byte("cambio\n"), 0o644)

	control := exec.Command("git", "-C", r.WT, "status", "--porcelain")
	control.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	_ = control.Run()
	if _, err := os.Stat(testigo); err != nil {
		t.Fatal("el control sin blindaje no ejecutó la config plantada: el test no prueba nada")
	}
	_ = os.Remove(testigo)
	if _, err := Sucios(context.Background(), r); !errors.Is(err, errWorktreeAjeno) {
		t.Fatalf("commondir plantado: %v", err)
	}
	if _, err := os.Stat(testigo); err == nil {
		t.Fatal("el git de la fábrica siguió el commondir plantado")
	}
	// Sin commondir plantado (solo la config del clon y el .gitattributes de la rama), git corre y nada
	// de lo plantado se ejecuta.
	_ = os.WriteFile(filepath.Join(gd, "commondir"), []byte("../..\n"), 0o644)
	if _, err := Sucios(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangedSince(context.Background(), r, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(testigo); err == nil {
		t.Fatal("se ejecutó algo plantado")
	}
	// Un core.fsmonitor en una config que git sí lee (la del clon, o la global del usuario): el
	// blindaje lo anula. El control lo ejecuta.
	if out, err := exec.Command("git", "-C", r.Src, "config", "core.fsmonitor", script).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	control = exec.Command("git", "-C", r.WT, "status", "--porcelain")
	_ = control.Run()
	if _, err := os.Stat(testigo); err != nil {
		t.Fatal("el control no ejecutó el fsmonitor del clon")
	}
	_ = os.Remove(testigo)
	if _, err := Sucios(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(testigo); err == nil {
		t.Fatal("el git status de la fábrica ejecutó core.fsmonitor")
	}
	_ = exec.Command("git", "-C", r.Src, "config", "--unset", "core.fsmonitor").Run()

	// config.worktree solo se lee con extensions.worktreeConfig en el clon (su config está negada a la
	// rama). Si aun así apareciera, la fábrica no corre git sobre ese worktree.
	_ = os.WriteFile(filepath.Join(gd, "config.worktree"), []byte(planta), 0o644)
	if out, err := exec.Command("git", "-C", r.Src, "config", "extensions.worktreeConfig", "true").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if _, err := Sucios(context.Background(), r); !errors.Is(err, errWorktreeAjeno) {
		t.Fatalf("con worktreeConfig: %v", err)
	}
	if _, err := os.Stat(testigo); err == nil {
		t.Fatal("el git de la fábrica ejecutó config.worktree")
	}
}

// El perfil del encierro niega los punteros del worktree real por ruta literal (srt así niega también
// mover sus carpetas padre).
func TestPerfilNiegaPunterosDelWorktree(t *testing.T) {
	home, r := worktreeDeTarea(t)
	c := Config{Home: home, Runs: filepath.Join(home, "runs")}
	b, _ := json.Marshal(c.perfilEncierro(dirTareaDe(r.WT), []Repo{r}, false))
	gd := gitdirDe(t, r)
	for _, p := range []string{filepath.Join(r.WT, ".git"), filepath.Join(gd, "commondir"), filepath.Join(gd, "gitdir"), filepath.Join(gd, "config.worktree")} {
		if !strings.Contains(string(b), `"`+p+`"`) {
			t.Errorf("falta denyWrite %s", p)
		}
	}
}
