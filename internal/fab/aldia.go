package fab

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Poner la rama al día con main. Si cada tarea partiera del main del momento en que nació y se
// reusara así en cada reintento, con varias tareas del mismo proyecto las PRs chocarían entre sí
// (dos migraciones con el mismo número, revisores que objetan código de PRs ya integradas). Por
// eso cada vuelta arranca rebasada sobre el main de GitHub.

// alDia rebasa la rama del worktree sobre origin/main (recién traído por ensureClone). Devuelve los
// archivos en conflicto si el rebase no entra solo; en ese caso lo aborta y deja la rama como estaba,
// para que el Builder lo resuelva con su contexto.
func alDia(ctx context.Context, r Repo, base string) (conflictos []string, err error) {
	// Un rebase que el Builder dejó a medias (se le acabó el turno con conflictos): se aborta y se
	// vuelve a intentar desde la rama como estaba, así el Builder recibe los archivos de nuevo.
	if rebaseAMedias(ctx, r) {
		if _, err := git(ctx, r.WT, "rebase", "--abort"); err != nil {
			return nil, fmt.Errorf("había un rebase a medias y no se pudo abortar: %w", err)
		}
	}
	if _, err := git(ctx, r.WT, "merge-base", "--is-ancestor", base, "HEAD"); err == nil {
		return nil, nil // ya sale del main de hoy
	}
	// Solo cuenta lo trackeado: un archivo suelto que dejó el chequeo no impide el rebase.
	if out, err := git(ctx, r.WT, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return nil, fmt.Errorf("no rebaso con cambios sin commitear: %s", out)
	}
	// Sin hooks del clon (pre-rebase, post-rewrite): la fábrica no corre código del repo al rebasar.
	if _, rerr := git(ctx, r.WT, "-c", "core.editor=true", "-c", "core.hooksPath=/dev/null", "rebase", base); rerr == nil {
		return nil, errRebaso
	}
	out, _ := git(ctx, r.WT, "diff", "--name-only", "--diff-filter=U")
	if _, err := git(ctx, r.WT, "rebase", "--abort"); err != nil {
		return nil, fmt.Errorf("el rebase falló y no se pudo abortar: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return nil, fmt.Errorf("el rebase sobre %s falló sin conflictos de archivos", base)
	}
	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

// rebaseAMedias: hay un rebase en curso en el worktree.
func rebaseAMedias(ctx context.Context, r Repo) bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		p, err := git(ctx, r.WT, "rev-parse", "--git-path", d)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.WT, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// errRebaso: no es una falla; avisa que la rama se movió (los tests congelados se vuelven a congelar
// sobre lo rebasado: main pudo cambiar limpio un archivo de aceptación que la tarea también tocó).
var errRebaso = errors.New("rebasada sobre main")

// conflictoMD es la tarea concreta que recibe el Builder cuando la rama choca con main.
func conflictoMD(repo, base string, archivos []string) string {
	return fmt.Sprintf("\n## La rama choca con main en %s\n"+
		"Mientras trabajabas, main cambió y estos archivos chocan con tu rama: %s.\n"+
		"Antes de seguir: en ese worktree corre `git rebase %s`, resuelve cada conflicto conservando lo de main "+
		"y lo tuyo (si las dos traen una migración con el mismo número, renumera la tuya a la siguiente libre), "+
		"`git add` y `git rebase --continue`. Después corre el chequeo.\n",
		repo, strings.Join(archivos, ", "), base)
}

// chocaConMain: los archivos en conflicto si sha se mergeara hoy en origin/main (sin tocar nada:
// merge-tree trabaja en memoria). Vacío si entra limpio o si no se pudo calcular.
func chocaConMain(ctx context.Context, clone, sha string) []string {
	if sha == "" {
		return nil
	}
	env, err := entornoGitSeguro(clone)
	if err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", clone, "merge-tree", "--write-tree", "--name-only", Base, sha)
	cmd.Env = conEntorno(os.Environ(), env)
	out, err := cmd.Output()
	var ee *exec.ExitError
	if err == nil || !errors.As(err, &ee) || ee.ExitCode() != 1 {
		return nil // limpio, o no se pudo calcular (no frena la aprobación por eso)
	}
	// Exit 1 = conflictos: primera línea el árbol, después los archivos hasta una línea vacía.
	lineas := strings.Split(strings.TrimSpace(string(out)), "\n")
	var choques []string
	for _, l := range lineas[1:] {
		if strings.TrimSpace(l) == "" {
			break
		}
		choques = append(choques, strings.TrimSpace(l))
	}
	return choques
}
