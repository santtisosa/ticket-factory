package fab

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=f@f", "-c", "user.name=f"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func escribir(t *testing.T, dir, f, txt string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, f), []byte(txt), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repoConRama arma "origin" (con main) y un clon con la rama de una tarea en su worktree.
func repoConRama(t *testing.T) (origin string, r Repo) {
	t.Helper()
	base := t.TempDir()
	origin = filepath.Join(base, "origin")
	sh(t, base, "init", "-q", "-b", "main", origin)
	escribir(t, origin, "a.txt", "uno\n")
	sh(t, origin, "add", ".")
	sh(t, origin, "commit", "-q", "-m", "base")
	clon := filepath.Join(base, "clon")
	sh(t, base, "clone", "-q", origin, clon)
	r = Repo{Name: "x", Src: clon, WT: filepath.Join(base, "wt")}
	sh(t, clon, "worktree", "add", "-q", "-b", "factory/t", r.WT, Base)
	return origin, r
}

func TestAlDia(t *testing.T) {
	ctx := context.Background()

	t.Run("main cambió otro archivo: rebasa sola", func(t *testing.T) {
		origin, r := repoConRama(t)
		escribir(t, r.WT, "x.aceptacion.test.js", "test CA-1\n")
		sh(t, r.WT, "add", ".")
		sh(t, r.WT, "commit", "-q", "-m", "tarea")
		escribir(t, origin, "b.txt", "otra PR\n")
		sh(t, origin, "add", ".")
		sh(t, origin, "commit", "-q", "-m", "otra PR")
		sh(t, r.Src, "fetch", "-q", "origin", "+main:refs/remotes/origin/main")
		if conf, err := alDia(ctx, r, Base); !errors.Is(err, errRebaso) || len(conf) != 0 {
			t.Fatalf("rebase limpio: %v %v", conf, err)
		}
		if choques := chocaConMain(ctx, r.Src, "HEAD"); choques != nil {
			t.Fatalf("rebasada entra limpio: %v", choques)
		}
		if _, err := os.Stat(filepath.Join(r.WT, "b.txt")); err != nil {
			t.Fatal("la rama tiene que tener lo de main después del rebase")
		}
	})

	t.Run("main y la tarea tocan la misma línea: conflicto, rebase abortado", func(t *testing.T) {
		origin, r := repoConRama(t)
		escribir(t, r.WT, "a.txt", "de la tarea\n")
		sh(t, r.WT, "commit", "-qam", "tarea")
		antes := sh(t, r.WT, "rev-parse", "HEAD")
		escribir(t, origin, "a.txt", "de main\n")
		sh(t, origin, "commit", "-qam", "main")
		sh(t, r.Src, "fetch", "-q", "origin", "+main:refs/remotes/origin/main")
		conf, err := alDia(ctx, r, Base)
		if err != nil || len(conf) != 1 || conf[0] != "a.txt" {
			t.Fatalf("conflicto en a.txt: %v %v", conf, err)
		}
		if despues := sh(t, r.WT, "rev-parse", "HEAD"); despues != antes {
			t.Fatal("con conflicto la rama queda como estaba")
		}
		if md := conflictoMD("x", Base, conf); md == "" {
			t.Fatal("el Builder recibe la tarea del conflicto")
		}
		if choques := chocaConMain(ctx, r.Src, antes[:len(antes)-1]); len(choques) != 1 || choques[0] != "a.txt" {
			t.Fatalf("la aprobación ve el choque con main: %v", choques)
		}
	})

	t.Run("rebase a medias del Builder: se aborta y se reintenta", func(t *testing.T) {
		origin, r := repoConRama(t)
		escribir(t, r.WT, "a.txt", "de la tarea\n")
		sh(t, r.WT, "commit", "-qam", "tarea")
		escribir(t, origin, "a.txt", "de main\n")
		sh(t, origin, "commit", "-qam", "main")
		sh(t, r.Src, "fetch", "-q", "origin", "+main:refs/remotes/origin/main")
		cmd := exec.Command("git", "-C", r.WT, "-c", "user.email=f@f", "-c", "user.name=f", "rebase", Base)
		_ = cmd.Run() // queda a medias, con a.txt en conflicto
		if !rebaseAMedias(ctx, r) {
			t.Fatal("tiene que ver el rebase a medias")
		}
		conf, err := alDia(ctx, r, Base)
		if err != nil || len(conf) != 1 || rebaseAMedias(ctx, r) {
			t.Fatalf("aborta y devuelve el conflicto de nuevo: %v %v", conf, err)
		}
	})

	t.Run("un archivo suelto sin trackear no frena el rebase", func(t *testing.T) {
		origin, r := repoConRama(t)
		escribir(t, r.WT, "c.txt", "tarea\n")
		sh(t, r.WT, "add", ".")
		sh(t, r.WT, "commit", "-qm", "tarea")
		escribir(t, r.WT, "basura.log", "salida del chequeo\n")
		escribir(t, origin, "b.txt", "main\n")
		sh(t, origin, "add", ".")
		sh(t, origin, "commit", "-qm", "main")
		sh(t, r.Src, "fetch", "-q", "origin", "+main:refs/remotes/origin/main")
		if conf, err := alDia(ctx, r, Base); !errors.Is(err, errRebaso) || len(conf) != 0 {
			t.Fatalf("rebasa igual: %v %v", conf, err)
		}
	})

	t.Run("ya sale de main: no hace nada", func(t *testing.T) {
		_, r := repoConRama(t)
		if conf, err := alDia(ctx, r, Base); err != nil || conf != nil {
			t.Fatalf("%v %v", conf, err)
		}
	})
}
