package fab

// Seguimiento de los issues de origen: cuando la fábrica toma una tarea que vino de github-issues, el
// issue recibe la etiqueta fabrica:en-curso; cuando la rama queda lista para la aprobación, pasa a
// fabrica:lista. Es la única escritura en GitHub previa a la aprobación y la hace el orquestador, no un
// agente. Un error se registra y la tarea sigue: el seguimiento no puede frenar el trabajo.

import (
	"context"
	"fmt"
	"regexp"
)

const (
	EtiquetaEnCurso = "fabrica:en-curso"
	EtiquetaLista   = "fabrica:lista"
)

// reOrigenIssue: owner/repo#n, tal como lo guarda github-issues en Entrada.Origen.
var reOrigenIssue = regexp.MustCompile(`^([\w.-]+/[\w.-]+)#(\d+)$`)

// issueDe: el repositorio y el número del issue de origen de una tarea, si vino de github-issues.
func issueDe(t *Task) (repo, numero string, ok bool) {
	var e Entrada
	if t.ReadJSON("00-entrada.json", &e) != nil {
		return "", "", false
	}
	m := reOrigenIssue.FindStringSubmatch(e.Origen)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// etiquetarIssue agrega una etiqueta al issue de origen y quita otra (vacía = ninguna).
func etiquetarIssue(ctx context.Context, t *Task, agregar, quitar string) {
	repo, n, ok := issueDe(t)
	if !ok {
		return
	}
	args := []string{"issue", "edit", n, "--repo", repo, "--add-label", agregar}
	if quitar != "" {
		args = append(args, "--remove-label", quitar)
	}
	if _, err := leerGH(ctx, args...); err != nil {
		t.Log("seguimiento", "falla", fmt.Sprintf("no se pudo etiquetar %s#%s con %s: %v", repo, n, agregar, err), 0)
	}
}
