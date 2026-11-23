package fab

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A mano: SOLOINFRA_RUNS=runs go test -run TestSoloInfraReal -v ./internal/fab/
func TestSoloInfraReal(t *testing.T) {
	runs := os.Getenv("SOLOINFRA_RUNS")
	if runs == "" {
		t.Skip("sin SOLOINFRA_RUNS")
	}
	archivos, _ := filepath.Glob(filepath.Join(runs, "20261104-*", "**", "04-approver-v*.json"))
	mas, _ := filepath.Glob(filepath.Join(runs, "20261104-*", "04-approver-v*.json"))
	for _, f := range append(archivos, mas...) {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var d Decision
		if json.Unmarshal(b, &d) != nil || d.Pasa {
			continue
		}
		t.Logf("%v  %s", SoloInfra(d), strings.TrimPrefix(f, runs+"/"))
	}
}

// A mano: SEMGREP_WT=<worktree con .semgrep> go test -run TestSemgrepConCleanEnv -v ./internal/fab/
func TestSemgrepConCleanEnv(t *testing.T) {
	wt := os.Getenv("SEMGREP_WT")
	if wt == "" {
		t.Skip("sin SEMGREP_WT")
	}
	linea := SemgrepCmd(Repo{WT: wt})
	cmd := exec.Command("bash", "-c", linea)
	cmd.Dir, cmd.Env = wt, cleanEnv()
	out, err := cmd.CombinedOutput()
	t.Logf("%s → err=%v %s", linea, err, out)
}

// A mano: AUTOMERGE_RUTAS=<archivo con líneas "id repo ruta"> go test -run TestRutaSensibleReal -v ./internal/fab/
// Cuenta las tareas de un repo que el merge solo dejaría pasar por la ruta (las demás condiciones aparte).
func TestRutaSensibleReal(t *testing.T) {
	lista := os.Getenv("AUTOMERGE_RUTAS")
	if lista == "" {
		t.Skip("sin AUTOMERGE_RUTAS")
	}
	b, err := os.ReadFile(lista)
	if err != nil {
		t.Fatal(err)
	}
	repos, marca := map[string]map[string]bool{}, map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		p := strings.SplitN(l, " ", 3)
		if len(p) < 3 {
			continue
		}
		if repos[p[0]] == nil {
			repos[p[0]] = map[string]bool{}
		}
		repos[p[0]][p[1]] = true
		if cat := patronSensible(p[2], nil); cat != "" && marca[p[0]] == "" {
			marca[p[0]] = cat + ": " + p[2]
		}
	}
	pasan := 0
	for id, rs := range repos {
		ok := len(rs) == 1 && marca[id] == ""
		if ok {
			pasan++
		}
		t.Logf("%s repos=%d pasa=%v %s", id, len(rs), ok, marca[id])
	}
	t.Logf("%d de %d tareas pasan por la ruta", pasan, len(repos))
}
