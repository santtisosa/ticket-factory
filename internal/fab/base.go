package fab

import "context"

// La base de la revisión (H2): el sha de origin/main contra el que se compara la rama. La fija la
// fábrica justo después de su propio fetch, antes de que corra ningún agente, y la guarda en
// runs/<id>/base.json (protegido como los demás json de la tarea). Escaneo, gitleaks, semgrep, el diff
// de los revisores y lo que se muestra al aprobar usan ese sha, nunca el ref origin/main: el ref vive
// en el clon, que el Builder escribe, y moverlo escondería commits de la revisión.

// BaseArchivo guarda repo → sha de la base.
const BaseArchivo = "base.json"

// baseDe: el sha fijado del repo, o el ref si no hay (probar-app, que trabaja sobre main).
func baseDe(r Repo) string {
	if r.BaseSHA != "" {
		return r.BaseSHA
	}
	return Base
}

// baseGuardada: el sha de base.json de ese repo, o "".
func baseGuardada(t *Task, repo string) string {
	var bs map[string]string
	if t.ReadJSON(BaseArchivo, &bs) != nil {
		return ""
	}
	return bs[repo]
}

// cargarBase pone en cada repo su base guardada. Un repo sin base (tarea de antes de base.json, o un
// worktree recién creado) toma el merge-base entre origin/main y HEAD, que en un worktree recién creado
// es el origin/main que la fábrica acaba de traer. Guarda el resultado.
func cargarBase(ctx context.Context, t *Task, repos []Repo) error {
	bs := map[string]string{}
	_ = t.ReadJSON(BaseArchivo, &bs)
	for i, r := range repos {
		if bs[r.Name] == "" {
			sha, err := git(ctx, r.WT, "merge-base", Base, "HEAD")
			if err != nil {
				return err
			}
			bs[r.Name] = sha
		}
		repos[i].BaseSHA = bs[r.Name]
	}
	return t.WriteJSON(BaseArchivo, bs)
}

// mainTraido: el sha de origin/main recién traído por la fábrica (ensureClone), leído antes de que
// corra un agente.
func mainTraido(ctx context.Context, r Repo) (string, error) {
	return git(ctx, r.WT, "rev-parse", "--verify", Base+"^{commit}")
}

// avanzarBase: la base pasa a candidato si la rama ya lo contiene (se rebasó encima). Guarda base.json.
// candidato siempre sale de mainTraido, nunca de leer el ref después de que trabajó un agente.
func avanzarBase(ctx context.Context, t *Task, repos []Repo, candidato map[string]string) error {
	bs := map[string]string{}
	_ = t.ReadJSON(BaseArchivo, &bs)
	cambio := false
	for i, r := range repos {
		sha := candidato[r.Name]
		if sha == "" || sha == repos[i].BaseSHA {
			continue
		}
		if _, err := git(ctx, r.WT, "merge-base", "--is-ancestor", sha, "HEAD"); err == nil {
			repos[i].BaseSHA, bs[r.Name], cambio = sha, sha, true
		}
	}
	if !cambio {
		return nil
	}
	return t.WriteJSON(BaseArchivo, bs)
}
