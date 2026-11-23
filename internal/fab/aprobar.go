package fab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Aprobar es el único camino por el que algo sale de la fábrica. Publica la rama de la tarea desde el
// clon privado al GitHub del repo y abre una PR lista para revisión con la spec, para que corra el CI.
// Se pide desde la terminal o el botón de la página, pero solo publica si una persona lo confirma por
// un canal que los agentes no alcanzan (confirmar.go); no-push.sh bloquea push y PR en los agentes.
// desdePanel: el pedido llegó por la página; el diálogo lo dice.
func (c Config) Aprobar(ctx context.Context, id string, dryRun, desdePanel bool) ([]string, error) {
	t, err := findTask(c.Runs, id)
	if err != nil {
		return nil, err
	}
	if t.Est.Estado != EsperandoAprobacion {
		return nil, fmt.Errorf("%s está en %s: solo se da la aprobación a una tarea en %s", id, t.Est.Estado, EsperandoAprobacion)
	}
	var spec Spec
	if err := t.ReadJSON("01-spec.json", &spec); err != nil {
		return nil, err
	}
	var aprobado map[string]string
	if err := t.ReadJSON("aprobado.json", &aprobado); err != nil {
		return nil, fmt.Errorf("%s no tiene commits aprobados (es de antes de esta regla o no pasó): %w", id, err)
	}
	cs := Clientes(c.Home)
	// Los repos del encabezado salen de la spec: antes del diálogo, que todos sean del espacio.
	if len(spec.Repos) == 0 {
		return nil, fmt.Errorf("%s: la spec no nombra repos", id)
	}
	for _, repo := range spec.Repos {
		if _, _, ok := buscarRepo(cs, repo); !ok {
			return nil, fmt.Errorf("el repo %q no es de ningún proyecto", repo)
		}
	}
	if !dryRun {
		detalle := "Tarea " + id + ". Publica la rama y abre el pull request."
		if e, err := CargarEspacio(c.Home); err == nil && e.Merge.Automatico {
			detalle += " Si " + e.Merge.CheckRequerido + " queda en verde y no toca archivos sensibles, se integra sola."
		}
		if err := confirmar(ctx, textoPublicar(spec.Repos, t.Est.Titulo, desdePanel, detalle), id); err != nil {
			return nil, err
		}
	}
	titulo, cuerpo := tituloPR(spec), cuerpoPR(t, spec)
	var lines []string
	urls := map[string]string{}
	_ = t.ReadJSON(PRsArchivo, &urls) // una aprobación que se cortó a mitad sigue donde quedó
	for _, repo := range spec.Repos {
		clone := filepath.Join(c.Home, "clones", repo)
		wt := filepath.Join(t.Dir, "wt", repo)
		if err := verificarAprobado(ctx, clone, wt, Branch(id), aprobado[repo]); err != nil {
			return lines, fmt.Errorf("%s: %w", repo, err)
		}
		rc, _, ok := buscarRepo(cs, repo)
		if !ok {
			return lines, fmt.Errorf("el repo %q no es de ningún proyecto", repo)
		}
		// Lo aprobado tiene que entrar limpio en el main de hoy: si otra PR del mismo proyecto se integró
		// mientras esperaba la aprobación, la PR nacería con conflictos.
		if err := ensureClone(ctx, rc.Ruta, clone); err == nil {
			if choques := chocaConMain(ctx, clone, aprobado[repo]); len(choques) > 0 {
				return lines, fmt.Errorf("%s: lo aprobado ya no entra limpio en main (chocan %s): `fabrica reintentar %s` la pone al día y la vuelve a revisar",
					repo, strings.Join(choques, ", "), id)
			}
		}
		remote, err := git(ctx, rc.Ruta, "remote", "get-url", "origin")
		if err != nil {
			return lines, err
		}
		slug, err := githubSlug(remote)
		if err != nil {
			return lines, err
		}
		destino, err := urlGitHub(remote)
		if err != nil {
			return lines, err
		}
		// Se sube el commit aprobado, no lo que tenga la rama: si alguien la movió, verificarAprobado ya frenó.
		// Siempre por https y con el token de la fábrica (comandoGitGitHub), nunca con las llaves del usuario.
		push := []string{"push", destino, aprobado[repo] + ":refs/heads/" + Branch(id)}
		crear := []string{"pr", "create", "--repo", slug, "--base", "main", "--head", Branch(id),
			"--title", titulo, "--body", cuerpo}
		if base := baseGuardada(t, repo); base != "" {
			lines = append(lines, repo+": base fija de la revisión "+base+", commit aprobado "+aprobado[repo])
		}
		lines = append(lines, "git "+strings.Join(push, " "), "gh "+strings.Join(crear[:len(crear)-1], " ")+" <cuerpo de "+fmt.Sprint(len(cuerpo))+" caracteres>")
		if rc.CIManual {
			lines = append(lines, "gh workflow run "+workflowCIManual+" --repo "+slug+" --ref "+Branch(id)+"   (ci_manual: el CI de este repo corre a mano)")
		}
		if dryRun {
			continue
		}
		cmd, err := comandoGitGitHub(ctx, clone, push...)
		if err != nil {
			return lines, fmt.Errorf("push de %s: %w", repo, err)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return lines, fmt.Errorf("push de %s: %w: %s", repo, err, tail(string(out)))
		}
		if urls[repo] == "" {
			urls[repo] = prExistente(ctx, slug, Branch(id))
			// Una PR de antes puede haber quedado en borrador: sin pasarla a lista, el CI no corre.
			if urls[repo] != "" {
				out, err := ghCombinada(ctx, "pr", "ready", urls[repo])
				if err != nil && !strings.Contains(string(out), "already") {
					return lines, fmt.Errorf("pasar a lista la PR de %s: %w: %s", repo, err, tail(string(out)))
				}
			}
		}
		if urls[repo] == "" {
			out, err := ghCombinada(ctx, crear...)
			if err != nil {
				return lines, fmt.Errorf("PR de %s: %w: %s", repo, err, tail(string(out)))
			}
			urls[repo] = ultimaLinea(string(out))
			if !strings.HasPrefix(urls[repo], "https://") {
				return lines, fmt.Errorf("gh no devolvió la URL de la PR de %s: %s", repo, tail(string(out)))
			}
		}
		if err := t.WriteJSON(PRsArchivo, urls); err != nil {
			return lines, err
		}
		// ci_manual: sin esto la PR queda sin checks y nadie se entera. Si no se puede disparar, la PR
		// ya existe: se avisa y se sigue.
		if rc.CIManual {
			out, err := ghCombinada(ctx, "workflow", "run", workflowCIManual, "--repo", slug, "--ref", Branch(id))
			if err != nil {
				lines = append(lines, fmt.Sprintf("no se pudo disparar el CI de %s (%s): %s", repo, workflowCIManual, tail(string(out))))
			} else {
				lines = append(lines, "CI disparado: "+workflowCIManual+" en "+repo)
			}
		}
	}
	if dryRun {
		lines = append(lines, "", "título: "+titulo, "", cuerpo)
		return lines, nil
	}
	var lista []string
	for _, repo := range spec.Repos {
		lista = append(lista, urls[repo])
	}
	t.Finish(PRAbierto, "PR abierta, corre el CI: "+strings.Join(lista, " "))
	StopPreviewDe(c.Runs, id)
	return lines, nil
}

// workflowCIManual es el workflow que se dispara a mano en los repos con ci_manual.
const workflowCIManual = "ci.yml"

func ultimaLinea(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(ls[len(ls)-1])
}

// verificarAprobado: la rama que se sube es exactamente el commit que pasó la revisión y el
// worktree no tiene nada sin commitear. Si el Builder (o cualquiera) tocó algo después, no se sube.
func verificarAprobado(ctx context.Context, clone, wt, rama, sha string) error {
	if sha == "" {
		return fmt.Errorf("no hay commit aprobado")
	}
	head, err := git(ctx, clone, "rev-parse", rama)
	if err != nil {
		return err
	}
	if head != sha {
		return fmt.Errorf("la rama %s está en %s pero lo aprobado es %s: algo cambió después de la revisión", rama, head, sha)
	}
	if sucios, err := Sucios(ctx, Repo{WT: wt}); err != nil || len(sucios) > 0 {
		return fmt.Errorf("el worktree tiene cambios sin commitear: %v %v", sucios, err)
	}
	// Sin reemplazos, grafts, alternates ni atributos plantados: lo que se sube es lo que se revisó.
	return integridadGit(ctx, clone)
}

// githubSlug saca owner/repo de un remote https o ssh de GitHub.
func githubSlug(remote string) (string, error) {
	r := strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	for _, p := range []string{"https://github.com/", "git@github.com:", "ssh://git@github.com/"} {
		if rest, ok := strings.CutPrefix(r, p); ok && strings.Count(rest, "/") == 1 {
			return rest, nil
		}
	}
	return "", fmt.Errorf("el remote %q no es de GitHub", remote)
}

func findTask(runs, id string) (*Task, error) {
	ts, err := LoadTasks(runs)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.Est.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("no existe la tarea %s", id)
}

// CerebroSync lleva lo aprendido al repo de conocimiento como una PR en borrador con la etiqueta
// factory, para que el operador la integre cuando quiera. Tampoco lo corre ningún agente. La rama se rebasa sobre main
// en cada pasada, así que se sube con --force-with-lease contra lo que GitHub tiene ahora: si alguien
// la tocó a mano en el medio, no se pisa.
func (c Config) CerebroSync(ctx context.Context, dryRun bool) (string, error) {
	soltar, err := c.bloquearCerebro()
	if err != nil {
		return "", err
	}
	defer soltar()
	if err := c.EnsureBrain(ctx); err != nil {
		return "", err
	}
	clone := c.BrainClone()
	if n, err := git(ctx, clone, "rev-list", "--count", Base+".."+brainBranch); err != nil {
		return "", err
	} else if n == "0" {
		return "nada que llevar: lo del cerebro ya está en el main del repo de conocimiento", nil
	}
	k, err := c.conocimiento()
	if err != nil {
		return "", err
	}
	remote := k.Repo
	if !esRemotoGitHub(remote) {
		if remote, err = git(ctx, k.Repo, "remote", "get-url", "origin"); err != nil {
			return "", err
		}
	}
	slug, err := githubSlug(remote)
	if err != nil {
		return "", err
	}
	destino, err := urlGitHub(remote)
	if err != nil {
		return "", err
	}
	const rama = "fabrica/cerebro"
	// El lease es contra lo que subió la fábrica la última vez (no contra lo que GitHub tiene ahora):
	// si alguien empujó un arreglo a la rama de la PR, el push falla en vez de pisarlo.
	ultimoPath := filepath.Join(c.Runs, ".cache", "cerebro-sync.sha")
	ultimo, _ := os.ReadFile(ultimoPath)
	push := []string{"push", "--force-with-lease=refs/heads/" + rama + ":" + strings.TrimSpace(string(ultimo)), destino, brainBranch + ":refs/heads/" + rama}
	crear := []string{"pr", "create", "--draft", "--repo", slug, "--base", "main", "--head", rama, "--label", "factory",
		"--title", "docs: lo que aprendió la fábrica (factory)",
		"--body", "Lecciones y notas que escribió el Bibliotecario de ticket-factory. Cada una lleva la marca `factory` y la tarea de donde salió. Se revisan como cualquier otra PR del repositorio."}
	line := "git " + strings.Join(push, " ")
	if dryRun {
		return line, nil
	}
	if err := confirmar(ctx, textoPublicar([]string{slug}, "Lo que aprendió la fábrica", false, "Hace push de "+rama+" y abre una PR en borrador."), "cerebro-sync"); err != nil {
		return "", err
	}
	cmd, err := comandoGitGitHub(ctx, clone, push...)
	if err != nil {
		return line, err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return line, fmt.Errorf("git push (si alguien tocó la rama %s en GitHub, hay que unirla a mano en clones/conocimiento): %w: %s", rama, err, tail(string(out)))
	}
	if sha, err := git(ctx, clone, "rev-parse", brainBranch); err == nil {
		_ = os.WriteFile(ultimoPath, []byte(sha), 0o644)
	}
	if url := prExistente(ctx, slug, rama); url != "" {
		return line + "\nPR ya abierta: " + url, nil
	}
	out, err := ghCombinada(ctx, crear...)
	if err != nil && strings.Contains(string(out), "label") {
		// Sin la etiqueta creada en el repo: la PR sale igual, con factory en el título.
		sin := append(append([]string{}, crear[:9]...), crear[11:]...)
		out, err = ghCombinada(ctx, sin...)
	}
	if err != nil {
		return line, fmt.Errorf("PR del cerebro: %w: %s", err, tail(string(out)))
	}
	return line + "\nPR: " + ultimaLinea(string(out)), nil
}
