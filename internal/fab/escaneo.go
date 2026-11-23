package fab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// La etapa determinista corre antes que los revisores LLM: lo que encuentra una herramienta bloquea
// sin depender de que un modelo lo vea. Una herramienta que no está o no corre no aprueba en
// silencio: queda "sin revisar" (infraPrefijo) y el operador lo ve antes de la aprobación.

const escaneoTimeout = 5 * time.Minute

// revisorHerramienta es el prefijo del revisor de los hallazgos de herramientas: cuentan como graves.
const revisorHerramienta = "herramienta:"

// Linea es una línea agregada en el diff de la rama contra main.
type Linea struct {
	Archivo string
	N       int
	Texto   string
}

var reHunk = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// lineasAgregadas parsea `git diff -U0`: solo lo que la rama agrega, con su número de línea.
func lineasAgregadas(diff string) []Linea {
	var out []Linea
	archivo, n, previa := "", 0, ""
	for _, l := range strings.Split(diff, "\n") {
		anterior := previa
		previa = l
		switch {
		// El encabezado +++ viene siempre después de ---: una línea agregada que empieza con "++ " no lo es.
		case strings.HasPrefix(l, "+++ ") && strings.HasPrefix(anterior, "--- "):
			archivo = strings.TrimPrefix(strings.TrimRight(strings.TrimPrefix(l, "+++ "), "\t"), "b/")
			if archivo == "/dev/null" {
				archivo = ""
			}
		case strings.HasPrefix(l, "@@"):
			if m := reHunk.FindStringSubmatch(l); m != nil {
				n, _ = strconv.Atoi(m[1])
			}
		case strings.HasPrefix(l, "+") && archivo != "":
			out = append(out, Linea{Archivo: archivo, N: n, Texto: strings.TrimPrefix(l, "+")})
			n++
		}
	}
	return out
}

// buscarBinario: PATH, y si no ~/go/bin (gosec y govulncheck se instalan ahí con go install).
func buscarBinario(nombre string) (string, bool) {
	if p, err := exec.LookPath(nombre); err == nil {
		return p, true
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, "go", "bin", nombre)
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	return "", false
}

// correrHerramienta corre una herramienta en el worktree dentro de srt, con el perfil de herramientas
// de la tarea (red solo a dependencias, vuln.go.dev y semgrep.dev; sin escribir el worktree), con tope,
// sin secretos y con el temporal y las cachés de la tarea. Lee código del Builder: nunca corre afuera.
// Devuelve stdout; el código de salida no importa si escribió algo.
func correrHerramienta(ctx context.Context, r Repo, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, escaneoTimeout)
	defer cancel()
	// gitleaks corre git sobre el worktree: con el mismo blindaje y gitdir verificado que la fábrica.
	gitEnv, err := entornoGitSeguro(r.WT)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(gitEnv, func(kv string) bool { return strings.HasPrefix(kv, "GIT_DIR=") }) {
		gd, err := git(ctx, r.WT, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return nil, err
		}
		gitEnv = append(gitEnv, "GIT_DIR="+gd, "GIT_WORK_TREE="+r.WT)
	}
	// Sin el temporal de la tarea, go list falla y gosec sale "limpio" con 0 archivos (probado).
	if err := crearDirsDeTarea(dirTareaDe(r.WT)); err != nil {
		return nil, err
	}
	linea := shQuote(bin)
	for _, a := range args {
		linea += " " + shQuote(a)
	}
	cmd, err := comandoEncerrado(ctx, perfilHerramientasDe(r.Encierro), linea)
	if err != nil {
		return nil, err
	}
	enGrupo(cmd)
	cmd.Dir = r.WT
	cmd.Env = append(conEntorno(cleanEnv(), gitEnv), entornoDeTarea(dirTareaDe(r.WT))...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	matarGrupo(cmd)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s no terminó en %s", filepath.Base(bin), escaneoTimeout)
	}
	if out.Len() == 0 && err != nil {
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(bin), err, tail(errb.String()))
	}
	return out.Bytes(), nil
}

// parseGosec: {"Issues":[{severity, confidence, rule_id, details, file, line, code}]}.
func parseGosec(repo, wt string, out []byte, agregadas map[string]map[int]bool) ([]Hallazgo, error) {
	var r struct {
		Errores map[string]any `json:"Golang errors"`
		Stats   *struct {
			Files int `json:"files"`
		} `json:"Stats"`
		Issues []struct {
			Severity   string `json:"severity"`
			Confidence string `json:"confidence"`
			RuleID     string `json:"rule_id"`
			Details    string `json:"details"`
			File       string `json:"file"`
			Line       string `json:"line"`
			Code       string `json:"code"`
		} `json:"Issues"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("gosec: salida ilegible: %w", err)
	}
	if len(r.Errores) > 0 {
		// Si no compiló algún paquete, gosec no lo revisó: eso no es un "no encontré nada".
		return nil, fmt.Errorf("gosec no pudo cargar %d paquete(s)", len(r.Errores))
	}
	if r.Stats != nil && r.Stats.Files == 0 {
		// Sin paquetes cargados (módulos que no bajaron, go.sum incompleto) gosec sale "limpio".
		return nil, fmt.Errorf("gosec no revisó ningún archivo")
	}
	conf := map[string]int{"HIGH": 8, "MEDIUM": 5, "LOW": 3}
	var hs []Hallazgo
	// gosec informa rutas reales (en macOS /var es /private/var): se compara contra las dos.
	wtReal := wt
	if real, err := filepath.EvalSymlinks(wt); err == nil {
		wtReal = real
	}
	for _, i := range r.Issues {
		rel, err := filepath.Rel(wt, i.File)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel, err = filepath.Rel(wtReal, i.File)
		}
		desde, hasta := rangoLineas(i.Line)
		if err != nil || !tocaAlguna(agregadas[rel], desde, hasta) {
			continue // solo lo que agregó la rama: lo viejo no es del Builder
		}
		linea := desde
		hs = append(hs, Hallazgo{Revisor: revisorHerramienta + "gosec", Regla: "gosec-" + i.RuleID, Severidad: i.Severity,
			Repo: repo, Archivo: rel, Linea: linea, Evidencia: strings.TrimSpace(i.Code), Detalle: i.Details, Confianza: conf[i.Confidence]})
	}
	return hs, nil
}

// parseGitleaks: un secreto commiteado es CRITICAL. El secreto no viaja al hallazgo (va a la PR y a
// los prompts): solo la regla y dónde. generic-api-key es ruidosa (claves de prueba): no bloquea.
func parseGitleaks(repo string, out []byte) ([]Hallazgo, error) {
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	var fs []struct {
		RuleID    string `json:"RuleID"`
		File      string `json:"File"`
		StartLine int    `json:"StartLine"`
		Commit    string `json:"Commit"`
	}
	if err := json.Unmarshal(out, &fs); err != nil {
		return nil, fmt.Errorf("gitleaks: salida ilegible: %w", err)
	}
	var hs []Hallazgo
	for _, f := range fs {
		sev, conf := "CRITICAL", 9
		if f.RuleID == "generic-api-key" {
			sev, conf = "MEDIUM", 4
		}
		hs = append(hs, Hallazgo{Revisor: revisorHerramienta + "gitleaks", Regla: "gitleaks-" + f.RuleID, Severidad: sev,
			Repo: repo, Archivo: f.File, Linea: f.StartLine, Confianza: conf,
			Evidencia: fmt.Sprintf("%s:%d en el commit %.10s (secreto oculto)", f.File, f.StartLine, f.Commit),
			Detalle:   "Hay un secreto commiteado en la rama. Sacarlo no alcanza: hay que reescribir esos commits y rotarlo."})
	}
	return hs, nil
}

// archivosSemgrep: solo los fuentes que tocó la rama (semgrep sobre todo el repo tarda minutos).
func archivosSemgrep(cambiados map[string]bool) []string {
	var fs []string
	for f := range cambiados {
		switch filepath.Ext(f) {
		case ".go", ".ts", ".tsx", ".js", ".mjs":
			fs = append(fs, f)
		}
	}
	sort.Strings(fs)
	return fs
}

// parseSemgrep: solo líneas que agregó la rama. Bloquea un ERROR con confianza alta de la regla; lo
// demás queda como pista para el revisor de seguridad (confianza baja: el aprobador no lo cuenta).
func parseSemgrep(repo string, out []byte, agregadas map[string]map[int]bool) ([]Hallazgo, error) {
	var r struct {
		Results []struct {
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
			} `json:"start"`
			End struct {
				Line int `json:"line"`
			} `json:"end"`
			Extra struct {
				Severity string `json:"severity"`
				Message  string `json:"message"`
				Lines    string `json:"lines"`
				Metadata struct {
					Confidence string `json:"confidence"`
				} `json:"metadata"`
			} `json:"extra"`
		} `json:"results"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("semgrep: salida ilegible: %w", err)
	}
	var hs []Hallazgo
	for _, i := range r.Results {
		if !tocaAlguna(agregadas[i.Path], i.Start.Line, i.End.Line) {
			continue
		}
		sev, conf := "MEDIUM", 4
		if i.Extra.Severity == "ERROR" && i.Extra.Metadata.Confidence == "HIGH" {
			sev, conf = "HIGH", 8
		}
		hs = append(hs, Hallazgo{Revisor: revisorHerramienta + "semgrep", Regla: "semgrep-" + i.CheckID, Severidad: sev,
			Repo: repo, Archivo: i.Path, Linea: i.Start.Line, Evidencia: strings.TrimSpace(i.Extra.Lines),
			Detalle: i.Extra.Message, Confianza: conf})
	}
	if len(hs) == 0 && len(r.Errors) > 0 && len(r.Results) == 0 {
		return nil, fmt.Errorf("semgrep no pudo revisar: %s", r.Errors[0].Message)
	}
	return hs, nil
}

// parseNpmAudit: solo dependencias directas que la rama agregó o cambió en package.json.
// ponytail: lo transitivo y lo que ya estaba en main no se marca; audit de base contra rama si hace falta.
func parseNpmAudit(repo string, out []byte, agregados string) ([]Hallazgo, error) {
	var r struct {
		Error *struct {
			Summary string `json:"summary"`
		} `json:"error"`
		Vulnerabilities map[string]struct {
			Severity string `json:"severity"`
			IsDirect bool   `json:"isDirect"`
			Range    string `json:"range"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("npm audit: salida ilegible: %w", err)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("npm audit no corrió: %s", r.Error.Summary)
	}
	var hs []Hallazgo
	for nombre, v := range r.Vulnerabilities {
		sev := strings.ToUpper(v.Severity)
		if !v.IsDirect || (sev != "HIGH" && sev != "CRITICAL") || !strings.Contains(agregados, `"`+nombre+`"`) {
			continue
		}
		hs = append(hs, Hallazgo{Revisor: revisorHerramienta + "npm", Regla: "dependencia-vulnerable", Severidad: sev,
			Repo: repo, Archivo: "package.json", Evidencia: nombre + " " + v.Range, Detalle: "Dependencia con vulnerabilidad conocida.", Confianza: 9})
	}
	return hs, nil
}

// parseGovulncheck: el -json es una secuencia de objetos. Cuenta un "finding" con función en la
// traza (el código de verdad llama lo vulnerable) de un módulo que la rama agregó o cambió en go.mod.
func parseGovulncheck(repo string, out []byte, agregados string) ([]Hallazgo, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var hs []Hallazgo
	visto := map[string]bool{}
	for {
		var m struct {
			Finding *struct {
				OSV   string `json:"osv"`
				Trace []struct {
					Module   string `json:"module"`
					Version  string `json:"version"`
					Function string `json:"function"`
				} `json:"trace"`
			} `json:"finding"`
		}
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("govulncheck: salida ilegible: %w", err)
		}
		f := m.Finding
		if f == nil || len(f.Trace) == 0 || f.Trace[0].Function == "" || visto[f.OSV] {
			continue
		}
		mod := f.Trace[0].Module
		if !strings.Contains(agregados, mod) {
			continue
		}
		visto[f.OSV] = true
		hs = append(hs, Hallazgo{Revisor: revisorHerramienta + "govulncheck", Regla: "vuln-" + f.OSV, Severidad: "HIGH",
			Repo: repo, Archivo: "go.mod", Evidencia: fmt.Sprintf("%s %s (%s)", mod, f.Trace[0].Version, f.Trace[0].Function),
			Detalle: "El código llama a una función con una vulnerabilidad conocida.", Confianza: 9})
	}
	return hs, nil
}

// dirHerramientas: la config de gitleaks de la fábrica y una carpeta vacía como .gitleaksignore. Bajo
// runs/.cache (que el encierro no escribe), con allowRead en el perfil de herramientas.
func (c Config) dirHerramientas() string { return filepath.Join(c.Runs, ".cache", "herramientas") }

// configGitleaks escribe (siempre igual) la config de gitleaks de la fábrica: las reglas por defecto,
// sin allowlist del repo. Devuelve su ruta y la de la carpeta vacía de ignorados.
func (c Config) configGitleaks() (string, string) {
	dir := c.dirHerramientas()
	ignorar := filepath.Join(dir, "gitleaksignore")
	_ = os.MkdirAll(ignorar, 0o755)
	cfg := filepath.Join(dir, "gitleaks.toml")
	_ = os.WriteFile(cfg, []byte("# config de gitleaks de ticket-factory: las reglas por defecto, sin allowlist de la rama\n[extend]\nuseDefault = true\n"), 0o644)
	return cfg, ignorar
}

// escaneo corre la etapa determinista sobre cada repo. Devuelve una Review por herramienta.
func (c Config) escaneo(ctx context.Context, repos []Repo) []Review {
	var out []Review
	for _, r := range repos {
		if esGo(r.WT) {
			bajarModulos(ctx, r.Encierro, r.WT) // gosec y govulncheck cargan los paquetes desde la caché de la tarea
		}
		diff, err := git(ctx, r.WT, "diff", "-U0", baseDe(r)+"..."+headDe(r))
		if err != nil {
			out = append(out, Review{Revisor: revisorHerramienta + "diff", Err: infraPrefijo + r.Name + ": " + err.Error()})
			continue
		}
		lineas := lineasAgregadas(diff)
		cambiados := map[string]bool{}
		agregadosEn := map[string]string{}
		porLinea := map[string]map[int]bool{}
		for _, l := range lineas {
			cambiados[l.Archivo] = true
			agregadosEn[l.Archivo] += l.Texto + "\n"
			if porLinea[l.Archivo] == nil {
				porLinea[l.Archivo] = map[int]bool{}
			}
			porLinea[l.Archivo][l.N] = true
		}
		if esGo(r.WT) {
			out = append(out, herramienta(ctx, r, "gosec", []string{"-fmt=json", "-no-fail", "./..."},
				func(b []byte) ([]Hallazgo, error) { return parseGosec(r.Name, r.WT, b, porLinea) }))
			if cambiados["go.mod"] {
				out = append(out, herramienta(ctx, r, "govulncheck", []string{"-format", "json", "./..."},
					func(b []byte) ([]Hallazgo, error) { return parseGovulncheck(r.Name, b, agregadosEn["go.mod"]) }))
			}
		}
		// Secretos en cualquier commit de la rama (aunque un commit posterior los borre: ya están en git).
		// Con la config de la fábrica: ni el .gitleaks.toml, ni el .gitleaksignore, ni los gitleaks:allow
		// de la rama esconden un hallazgo. gitleaks lee además el .gitleaksignore de la carpeta que
		// escanea aunque se le indique otro (probado con 8.30.1): la carpeta es una vacía de la fábrica y
		// git encuentra el repo por GIT_DIR. El informe sale por stdout (--report-path -).
		cfg, ignorar := c.configGitleaks()
		out = append(out, herramienta(ctx, r, "gitleaks", []string{"git", ignorar, "--log-opts=" + baseDe(r) + ".." + headDe(r),
			"--config", cfg, "--gitleaks-ignore-path", ignorar, "--ignore-gitleaks-allow",
			"--report-format", "json", "--report-path", "-", "--exit-code", "0", "--no-banner"},
			func(b []byte) ([]Hallazgo, error) { return parseGitleaks(r.Name, b) }))
		if fuentes := archivosSemgrep(cambiados); len(fuentes) > 0 {
			// Reglas del registro elegidas por la fábrica; ni los nosemgrep ni el .semgrepignore ni el
			// .gitignore de la rama sacan archivos o líneas de la revisión.
			out = append(out, herramienta(ctx, r, "semgrep", append([]string{"scan", "--config", "p/golang",
				"--config", "p/typescript", "--config", "p/owasp-top-ten", "--disable-nosem", "--x-ignore-semgrepignore-files",
				"--no-git-ignore", "--json", "--metrics=off", "--quiet"}, fuentes...),
				func(b []byte) ([]Hallazgo, error) { return parseSemgrep(r.Name, b, porLinea) }))
		}
		if cambiados["package.json"] {
			out = append(out, herramienta(ctx, r, "npm", []string{"audit", "--json", "--omit=dev"},
				func(b []byte) ([]Hallazgo, error) { return parseNpmAudit(r.Name, b, agregadosEn["package.json"]) }))
		}
	}
	for i := range out {
		for j := range out[i].Hallazgos {
			out[i].Hallazgos[j].Fingerprint = Fingerprint(out[i].Hallazgos[j])
		}
	}
	return out
}

// herramienta corre una y la vuelve Review; si no está o no corre, queda sin revisar.
func herramienta(ctx context.Context, r Repo, nombre string, args []string, parse func([]byte) ([]Hallazgo, error)) Review {
	rv := Review{Revisor: revisorHerramienta + nombre, Veredicto: "ok"}
	bin, ok := buscarBinario(nombre)
	if !ok {
		rv.Err = infraPrefijo + nombre + " no está instalado"
		return rv
	}
	b, err := correrHerramienta(ctx, r, bin, args...)
	if err == nil {
		rv.Hallazgos, err = parse(b)
	}
	if err != nil {
		rv.Err = infraPrefijo + r.Name + ": " + err.Error()
	}
	return rv
}

// resumenHerramientas es lo que ven los revisores LLM: lo que ya marcaron las herramientas, para
// que no lo repitan y se ocupen de lo que una regla no ve.
func resumenHerramientas(rs []Review) string {
	var b strings.Builder
	for _, r := range rs {
		for _, h := range r.Hallazgos {
			fmt.Fprintf(&b, "- %s %s %s/%s:%d %s\n", strings.TrimPrefix(h.Revisor, revisorHerramienta), h.Regla, h.Repo, h.Archivo, h.Linea, h.Severidad)
		}
	}
	if b.Len() == 0 {
		return "\n## Herramientas\nLas herramientas (gosec, govulncheck, semgrep, npm audit) no encontraron nada en el diff.\n"
	}
	return "\n## Herramientas\nEsto ya lo marcaron las herramientas y bloquea solo; no lo repitas:\n" + b.String()
}

// rangoLineas: gosec da "12" o "12-15".
func rangoLineas(s string) (int, int) {
	a, b, _ := strings.Cut(s, "-")
	desde, _ := strconv.Atoi(a)
	hasta, err := strconv.Atoi(b)
	if err != nil {
		hasta = desde
	}
	return desde, hasta
}

func tocaAlguna(lineas map[int]bool, desde, hasta int) bool {
	for n := desde; n <= hasta; n++ {
		if lineas[n] {
			return true
		}
	}
	return false
}

// cambiosDeCI: un cambio en .github/ corre con los secretos del repo apenas se da la aprobación (la PR es
// del mismo repo, no un fork). No bloquea, pero queda "a confirmar": el operador lo ve antes de la aprobación.
// Sin evidencia a propósito: así clasificar lo manda a confirmar y no le quema la vuelta al Builder.
func cambiosDeCI(repo string, archivos []string) []Hallazgo {
	var hs []Hallazgo
	for _, f := range archivos {
		if !strings.HasPrefix(f, ".github/") {
			continue
		}
		h := Hallazgo{
			Revisor: revisorHerramienta + "ci", Severidad: "HIGH", Confianza: 10, Regla: "cambia-el-ci",
			Repo: repo, Archivo: f,
			Detalle: "La tarea cambia el CI: ese código corre con los secretos del repo cuando se da la aprobación. Conviene revisarlo antes.",
		}
		h.Fingerprint = Fingerprint(h)
		hs = append(hs, h)
	}
	return hs
}
