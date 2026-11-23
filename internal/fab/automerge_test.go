package fab

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prDePrueba(sha string, archivos ...string) prParaMerge {
	pr := prParaMerge{Number: 18, Mergeable: "MERGEABLE", HeadRefOid: sha, HeadRefName: Branch("t1"), ChangedFiles: len(archivos)}
	pr.Checks = []checkDeMerge{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS"}}
	for _, a := range archivos {
		pr.Files = append(pr.Files, struct {
			Path string `json:"path"`
		}{a})
	}
	return pr
}

func TestCoincideGlob(t *testing.T) {
	casos := []struct {
		patron, ruta string
		quiere       bool
	}{
		{"**/migrations/**", "migrations/0001.sql", true},
		{"**/migrations/**", "internal/db/migrations/0042_x.sql", true},
		{"**/migraciones/**", "a/b/migraciones/x.go", true},
		{"**/migrations/**", "internal/migrationsx/a.go", false},
		{".github/**", ".github/workflows/ci.yml", true},
		{".github/**", "docs/.github/x", false},
		{"package.json", "web/package.json", true},
		{"package.json", "package.json.bak", false},
		{"**/auth/**", "internal/auth/x.go", true},
		{"internal/*.go", "internal/a.go", true},
		{"internal/*.go", "internal/sub/a.go", false},
		{"**", "cualquier/cosa", true},
	}
	for _, c := range casos {
		if got, err := coincideGlob(c.patron, c.ruta); got != c.quiere || err != nil {
			t.Errorf("%s ~ %s = %v (%v), quería %v", c.patron, c.ruta, got, err, c.quiere)
		}
	}
	for _, p := range []string{"[mal", "a/[mal", "**/b/[x"} {
		if globValido(p) {
			t.Errorf("%q no es válido", p)
		}
	}
	if !globValido("**/auth/**") {
		t.Error("**/auth/** es válido")
	}
	if p := patronSensible("a.go", []string{"[mal"}); p != "[mal" {
		t.Errorf("un patrón inválido cuenta como sensible: %q", p)
	}
}

func TestGuardiaMerge(t *testing.T) {
	base := func(checks ...checkDeMerge) prParaMerge { p := prDePrueba("abc", "a.go"); p.Checks = checks; return p }
	ok := checkDeMerge{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS"}
	casos := []struct {
		nombre string
		pr     prParaMerge
		sha    string
		quiere string
	}{
		{"verde", base(ok), "abc", ""},
		{"status context verde", base(checkDeMerge{Context: "ci", State: "SUCCESS"}), "abc", ""},
		{"otro check omitido", base(ok, checkDeMerge{Name: "lint", Status: "COMPLETED", Conclusion: "SKIPPED"}), "abc", ""},
		{"sin el requerido", base(checkDeMerge{Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"}), "abc", "no aparece"},
		{"requerido rojo", base(checkDeMerge{Name: "ci", Status: "COMPLETED", Conclusion: "FAILURE"}), "abc", "ci no está en verde"},
		{"requerido repetido, uno rojo", base(ok, checkDeMerge{Name: "ci", Status: "COMPLETED", Conclusion: "FAILURE"}), "abc", "ci no está en verde"},
		{"otro rojo", base(ok, checkDeMerge{Name: "e2e", Status: "COMPLETED", Conclusion: "FAILURE"}), "abc", "e2e no está en verde"},
		{"otro corriendo", base(ok, checkDeMerge{Name: "e2e", Status: "IN_PROGRESS"}), "abc", "todavía corre"},
		{"status pendiente", base(ok, checkDeMerge{Context: "deploy/preview", State: "PENDING"}), "abc", "todavía corre"},
		{"head distinto", base(ok), "otro", "commit aprobado"},
		{"sin aprobado", base(ok), "", "commit aprobado"},
	}
	for _, c := range casos {
		got := guardiaMerge(c.pr, "ci", c.sha)
		if (c.quiere == "") != (got == "") || !strings.Contains(got, c.quiere) {
			t.Errorf("%s: %q, quería %q", c.nombre, got, c.quiere)
		}
	}
}

// Solo se integra sola una tarea de un repo, sin nada a confirmar ni sin revisar, con el commit
// aprobado, el check requerido en verde y sin tocar rutas sensibles.
func TestMotivoSinAutoMerge(t *testing.T) {
	uno := map[string]string{"demo-web": "https://github.com/organizacion/demo-web/pull/18"}
	aprob := map[string]string{"demo-web": "abc", "demo-api": "abc"}
	m := Merge{Automatico: true, CheckRequerido: "ci", Sensibles: []string{"**/auth/**"}}
	casos := []struct {
		nombre string
		d      Decision
		urls   map[string]string
		pr     prParaMerge
		quiere string
	}{
		{"limpia", Decision{}, uno, prDePrueba("abc", "app/inicio/page.tsx"), ""},
		{"dos repos", Decision{}, map[string]string{"a": "x", "b": "y"}, prDePrueba("abc", "a.go"), "más de un repo"},
		{"sensible configurado", Decision{}, uno, prDePrueba("abc", "internal/auth/x.go"), "**/auth/**"},
		{"toca el CI", Decision{}, uno, prDePrueba("abc", ".github/workflows/ci.yml"), ".github/**"},
		{"migración en la raíz", Decision{}, uno, prDePrueba("abc", "migrations/0042_x.sql"), "migrations"},
		{"migración en castellano", Decision{}, uno, prDePrueba("abc", "db/migraciones/embed.go"), "migraciones"},
		{"dependencias anidadas", Decision{}, uno, prDePrueba("abc", "web/package.json"), "package.json"},
		{"go.sum", Decision{}, uno, prDePrueba("abc", "go.sum"), "go.sum"},
		{"a confirmar", Decision{AConfirmar: []Hallazgo{{}}}, uno, prDePrueba("abc", "a.go"), "a confirmar"},
		{"sin revisar", Decision{SinRevisar: []string{"ux"}}, uno, prDePrueba("abc", "a.go"), "sin revisar"},
		{"conflicto", Decision{}, uno, prParaMerge{Mergeable: "CONFLICTING", HeadRefOid: "abc"}, "mergeable"},
		{"commit ajeno", Decision{}, uno, prDePrueba("otro", "a.go"), "commit aprobado"},
		{"otra rama", Decision{}, uno, func() prParaMerge { p := prDePrueba("abc", "a.go"); p.HeadRefName = "main"; return p }(), "rama"},
		{"sin el check requerido", Decision{}, uno, func() prParaMerge { p := prDePrueba("abc", "a.go"); p.Checks = nil; return p }(), "ci no está en verde"},
		{"archivos de más", Decision{}, uno, func() prParaMerge { p := prDePrueba("abc", "a.go"); p.ChangedFiles = 150; return p }(), "todos los archivos"},
		{"con deploy", Decision{}, map[string]string{"demo-api": "https://github.com/organizacion/demo-api/pull/31"},
			prDePrueba("abc", "x.go"), "se despliega al integrar"},
	}
	for _, c := range casos {
		got := motivoSinAutoMerge("t1", c.d, c.urls, aprob, c.pr, map[string]bool{"demo-api": true}, m)
		if (c.quiere == "") != (got == "") || !strings.Contains(got, c.quiere) {
			t.Errorf("%s: %q, quería %q", c.nombre, got, c.quiere)
		}
	}
}

func espacioConMerge(t *testing.T, merge string) string {
	t.Helper()
	home := t.TempDir()
	yml := "proyectos:\n  demo:\n    repos:\n      - nombre: demo-web\n        ruta: /tmp/demo-web\n" + merge
	if err := os.WriteFile(filepath.Join(home, ArchivoEspacio), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// Con merge.automatico, la integración usa --match-head-commit con el commit aprobado y se intenta
// una sola vez; sin él, no se lee ni se integra nada.
func TestAutoMerge(t *testing.T) {
	runs := t.TempDir()
	tk, err := AddTask(runs, "x", "prompt", CIVerde, "demo")
	if err != nil {
		t.Fatal(err)
	}
	tk.Est.Vuelta = 1
	_ = tk.WriteJSON("00-entrada.json", Entrada{})
	_ = tk.WriteJSON("04-approver-v1.json", Decision{Pasa: true})
	_ = tk.WriteJSON("aprobado.json", map[string]string{"demo-web": "abc"})
	urls := map[string]string{"demo-web": "https://github.com/organizacion/demo-web/pull/18"}

	viejo := leerGH
	t.Cleanup(func() { leerGH = viejo })
	var llamadas [][]string
	checks := `[{"__typename":"CheckRun","name":"ci","status":"COMPLETED","conclusion":"SUCCESS"}]`
	leerGH = func(_ context.Context, args ...string) ([]byte, error) {
		llamadas = append(llamadas, args)
		if args[1] == "merge" {
			return nil, nil
		}
		return []byte(`{"number":18,"mergeable":"MERGEABLE","headRefOid":"abc","headRefName":"` + Branch(tk.Est.ID) +
			`","changedFiles":1,"files":[{"path":"app/x.tsx"}],"statusCheckRollup":` + checks + `}`), nil
	}

	apagado := Config{Runs: runs, Home: espacioConMerge(t, "merge:\n  automatico: false\n  check_requerido: ci\n")}
	if l := apagado.autoMerge(context.Background(), tk, urls); l != "" || len(llamadas) != 0 {
		t.Fatalf("apagado: %q, llamadas=%v", l, llamadas)
	}

	c := Config{Runs: runs, Home: espacioConMerge(t, "merge:\n  automatico: true\n  check_requerido: ci\n")}
	if l := c.autoMerge(context.Background(), tk, urls); !strings.Contains(l, "integrada sola") {
		t.Fatalf("%q", l)
	}
	merge := strings.Join(llamadas[len(llamadas)-1], " ")
	if merge != "pr merge 18 --repo organizacion/demo-web --squash --match-head-commit abc" {
		t.Fatalf("merge: %s", merge)
	}
	n := len(llamadas)
	if l := c.autoMerge(context.Background(), tk, urls); l != "" || len(llamadas) != n {
		t.Fatalf("se intenta una sola vez: %q", l)
	}
}

// Con el check requerido en rojo no se llama a gh pr merge.
func TestAutoMergeSinCheckVerde(t *testing.T) {
	runs := t.TempDir()
	tk, _ := AddTask(runs, "x", "prompt", CIVerde, "demo")
	tk.Est.Vuelta = 1
	_ = tk.WriteJSON("04-approver-v1.json", Decision{Pasa: true})
	_ = tk.WriteJSON("aprobado.json", map[string]string{"demo-web": "abc"})
	viejo := leerGH
	t.Cleanup(func() { leerGH = viejo })
	merges := 0
	leerGH = func(_ context.Context, args ...string) ([]byte, error) {
		if args[1] == "merge" {
			merges++
		}
		return []byte(`{"mergeable":"MERGEABLE","headRefOid":"abc","headRefName":"` + Branch(tk.Est.ID) +
			`","changedFiles":1,"files":[{"path":"a.go"}],"statusCheckRollup":[{"name":"ci","status":"COMPLETED","conclusion":"FAILURE"}]}`), nil
	}
	c := Config{Runs: runs, Home: espacioConMerge(t, "merge:\n  automatico: true\n  check_requerido: ci\n")}
	if l := c.autoMerge(context.Background(), tk, map[string]string{"demo-web": "https://github.com/organizacion/demo-web/pull/18"}); !strings.Contains(l, "ci no está en verde") || merges != 0 {
		t.Fatalf("%q merges=%d", l, merges)
	}
}

// Lo que corre al construir o probar y los lockfiles también son sensibles, sin distinguir mayúsculas.
func TestSensiblesFijosAmpliados(t *testing.T) {
	for _, r := range []string{"Makefile", "web/makefile", "Dockerfile", "api/Dockerfile.dev", "docker-compose.yml",
		"compose.yaml", "infra/compose.test.yml", "yarn.lock", "pnpm-lock.yaml", ".npmrc", "web/.NPMRC",
		"hooks/pre-push", "tools/hooks/x.sh", "PACKAGE.JSON", ".GitHub/workflows/ci.yml"} {
		if patronSensible(r, nil) == "" {
			t.Errorf("%s no cuenta como sensible", r)
		}
	}
	for _, r := range []string{"web/src/App.tsx", "internal/x.go", "docs/compose.md"} {
		if p := patronSensible(r, nil); p != "" {
			t.Errorf("%s no es sensible (%s)", r, p)
		}
	}
}
