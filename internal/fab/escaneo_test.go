package fab

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoConDiff: un repo Go con origin/main vacío y una rama que agrega venta.go con ese código.
func repoConDiff(t *testing.T, codigo string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "base")
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "venta.go"), []byte(codigo), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "tarea")
	return dir
}

const ventaMala = `package venta

import (
	"database/sql"
	"fmt"
)

type Venta struct {
	Monto float64
}

func Buscar(db *sql.DB, cliente string) (*sql.Rows, error) {
	return db.Query(fmt.Sprintf("SELECT * FROM ventas WHERE cliente = '%s'", cliente))
}
`

func TestLineasAgregadas(t *testing.T) {
	// La línea agregada "++ x" no es un encabezado: no cambia el archivo.
	diff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -3,0 +4,2 @@\n+uno\n+++ x\n@@ -9 +11 @@\n-viejo\n+tres\n"
	ls := lineasAgregadas(diff)
	if len(ls) != 3 || ls[0].N != 4 || ls[1].N != 5 || ls[1].Texto != "++ x" || ls[2].N != 11 || ls[2].Archivo != "x.go" {
		t.Fatalf("%+v", ls)
	}
}

func TestParseNpmAuditSoloLoAgregado(t *testing.T) {
	out := []byte(`{"vulnerabilities":{"lodash":{"severity":"high","isDirect":true,"range":"<4.17.21"},
		"next":{"severity":"critical","isDirect":true,"range":"<16.4.3"},"minimist":{"severity":"high","isDirect":false}}}`)
	hs, err := parseNpmAudit("r", out, `    "lodash": "4.17.0",`)
	if err != nil || len(hs) != 1 || hs[0].Evidencia != "lodash <4.17.21" {
		t.Fatalf("%+v %v", hs, err)
	}
}

func TestParseGovulncheck(t *testing.T) {
	out := []byte(`{"config":{}}
{"finding":{"osv":"GO-1","trace":[{"module":"golang.org/x/net","version":"v0.1.0","function":"Parse"}]}}
{"finding":{"osv":"GO-2","trace":[{"module":"golang.org/x/net","version":"v0.1.0"}]}}
{"finding":{"osv":"GO-3","trace":[{"module":"github.com/viejo/mod","version":"v1","function":"F"}]}}
`)
	hs, err := parseGovulncheck("r", out, "\tgolang.org/x/net v0.1.0\n")
	if err != nil || len(hs) != 1 || hs[0].Regla != "vuln-GO-1" {
		t.Fatalf("%+v %v", hs, err)
	}
}

// Una herramienta que no está no aprueba en silencio: queda sin revisar.
func TestHerramientaQueFaltaQuedaSinRevisar(t *testing.T) {
	rv := herramienta(context.Background(), Repo{Name: "r", WT: t.TempDir()}, "no-existe-esta-herramienta", nil, nil)
	d := Decide(1, specWith("CA-1"), buildCovering("CA-1"), []Check{{OK: true}}, []Review{rv}, nil, nil)
	if len(d.SinRevisar) != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestParseGosecSoloLineasAgregadas(t *testing.T) {
	out := []byte(`{"Golang errors":{},"Issues":[{"severity":"HIGH","confidence":"HIGH","rule_id":"G201","details":"sql","file":"/wt/a.go","line":"10","code":"x"},
		{"severity":"HIGH","confidence":"HIGH","rule_id":"G101","details":"cred","file":"/wt/a.go","line":"30-32","code":"y"}]}`)
	hs, err := parseGosec("r", "/wt", out, map[string]map[int]bool{"a.go": {31: true}})
	if err != nil || len(hs) != 1 || hs[0].Regla != "gosec-G101" {
		t.Fatalf("%+v %v", hs, err)
	}
	if _, err := parseGosec("r", "/wt", []byte(`{"Golang errors":{"p":[{"error":"no compila"}]},"Issues":[]}`), nil); err == nil {
		t.Fatal("un paquete que no cargó no es un ok")
	}
	if _, err := parseNpmAudit("r", []byte(`{"error":{"code":"ENOLOCK","summary":"sin lockfile"}}`), ""); err == nil {
		t.Fatal("un audit que no corrió no es un ok")
	}
}

// Un secreto commiteado bloquea, pero el secreto no viaja al hallazgo; la regla genérica no bloquea.
func TestParseGitleaks(t *testing.T) {
	out := []byte(`[{"RuleID":"aws-access-token","File":"a.go","StartLine":2,"Secret":"` + claveFalsa + `","Match":"` + claveFalsa + `","Commit":"e4886f328ca67864"},
		{"RuleID":"generic-api-key","File":"t.ts","StartLine":9,"Secret":"x","Commit":"e4886f328ca67864"}]`)
	hs, err := parseGitleaks("r", out)
	if err != nil || len(hs) != 2 {
		t.Fatal(hs, err)
	}
	if !bloquea(hs[0]) || bloquea(hs[1]) {
		t.Errorf("aws bloquea y generic no: %+v", hs)
	}
	b, _ := json.Marshal(hs)
	if strings.Contains(string(b), claveFalsa) {
		t.Error("el secreto viajó al hallazgo")
	}
	if hs, err := parseGitleaks("r", nil); err != nil || hs != nil {
		t.Error("sin hallazgos, gitleaks no escribe nada", hs, err)
	}
}

// semgrep: solo líneas agregadas; bloquea un ERROR de confianza alta, lo demás es pista.
func TestParseSemgrep(t *testing.T) {
	out := []byte(`{"results":[
		{"check_id":"go.sqli","path":"b.go","start":{"line":3},"end":{"line":3},"extra":{"severity":"ERROR","message":"SQL","lines":"db.Query(x)","metadata":{"confidence":"HIGH"}}},
		{"check_id":"go.weak","path":"b.go","start":{"line":4},"end":{"line":4},"extra":{"severity":"WARNING","message":"m","lines":"y","metadata":{"confidence":"LOW"}}},
		{"check_id":"go.viejo","path":"b.go","start":{"line":9},"end":{"line":9},"extra":{"severity":"ERROR","message":"m","lines":"z","metadata":{"confidence":"HIGH"}}}],"errors":[]}`)
	hs, err := parseSemgrep("r", out, map[string]map[int]bool{"b.go": {3: true, 4: true}})
	if err != nil || len(hs) != 2 {
		t.Fatal(hs, err)
	}
	if !bloquea(hs[0]) || bloquea(hs[1]) {
		t.Errorf("%+v", hs)
	}
	if _, err := parseSemgrep("r", []byte(`{"results":[],"errors":[{"message":"rules: network"}]}`), nil); err == nil {
		t.Error("sin reglas no es un 'no encontré nada'")
	}
	if got := archivosSemgrep(map[string]bool{"a.go": true, "x.md": true, "c.tsx": true}); strings.Join(got, ",") != "a.go,c.tsx" {
		t.Error(got)
	}
}

// claveFalsa: una clave de AWS de ejemplo, armada en tiempo de ejecución para que el escaneo de secretos
// del propio repo no la marque.
var claveFalsa = "AKIA" + "IOSFODNN7EXAMPLQ"
