package fab

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// espacioConPreview: un proyecto acme con un backend y un frontend; preview es el bloque YAML de la
// receta (vacío = sin receta).
func espacioConPreview(t *testing.T, preview string) string {
	t.Helper()
	home := t.TempDir()
	yml := "proyectos:\n  acme:\n    repos:\n      - nombre: acme-api\n        ruta: /tmp/acme-api\n" +
		"      - nombre: acme-web\n        ruta: /tmp/acme-web\n        frontend: true\n" + preview
	if err := os.WriteFile(filepath.Join(home, ArchivoEspacio), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func runsDePrueba(t *testing.T) string {
	t.Helper()
	runs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	return runs
}

// servidorEnPuerto: una app de mentira ya escuchando, y puertoLibre devuelve su puerto. Así la receta
// real (bash) corre y la espera HTTP se cumple.
func servidorEnPuerto(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(srv.Close)
	puerto := srv.Listener.Addr().(*net.TCPAddr).Port
	viejo := puertoLibre
	t.Cleanup(func() { puertoLibre = viejo })
	puertoLibre = func() (int, error) { return puerto, nil }
	return puerto
}

// esperarMuerto: el líder del grupo ya no existe.
func esperarMuerto(t *testing.T, pid int) {
	t.Helper()
	for limite := time.Now().Add(15 * time.Second); time.Now().Before(limite); time.Sleep(100 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	t.Fatalf("el proceso %d sigue vivo", pid)
}

func leerCuandoEste(t *testing.T, ruta string) string {
	t.Helper()
	for limite := time.Now().Add(10 * time.Second); time.Now().Before(limite); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(ruta); err == nil && strings.HasSuffix(string(b), "\n") {
			return string(b)
		}
	}
	t.Fatalf("la receta no escribió %s", ruta)
	return ""
}

// Sin `preview:` en el proyecto, el motivo lo dice y no se baja la vista previa de otra tarea.
func TestPreviewSinReceta(t *testing.T) {
	c := Config{Home: espacioConPreview(t, ""), Runs: runsDePrueba(t)}
	if _, _, err := c.recetaDe([]string{"acme-web"}); err == nil || !strings.Contains(err.Error(), "no declara preview en fabrica.yaml") {
		t.Fatalf("motivo: %v", err)
	}
	if err := savePreview(c.Runs, &Preview{ID: "otra", URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	task, err := AddTask(c.Runs, "x", "ticket", Pendiente, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.WriteJSON("01-spec.json", Spec{Repos: []string{"acme-web"}}); err != nil {
		t.Fatal(err)
	}
	p := c.StartPreview(context.Background(), task)
	if !strings.Contains(p.Error, "no declara preview") {
		t.Fatalf("error: %+v", p)
	}
	if otra := LoadPreview(c.Runs); otra == nil || otra.ID != "otra" {
		t.Fatalf("bajó la vista previa de otra tarea: %+v", otra)
	}
}

// La receta corre en bash con {puerto} reemplazado y las variables de los servicios, sin secretos.
func TestArrancarAppPuertoYEntorno(t *testing.T) {
	t.Setenv("FABRICA_SMTP_CLAVE", "secreto")
	t.Setenv("FABRICA_APP_CLAVE", "secreto")
	puerto := servidorEnPuerto(t)
	dir := t.TempDir()
	receta := RecetaPreview{Comandos: []string{
		`echo "{puerto} $DB_URL ${FABRICA_SMTP_CLAVE:-nada} ${FABRICA_APP_CLAVE:-nada}" > salida.txt; sleep 60`,
	}}
	a, err := arrancarApp(context.Background(), receta, dir, []string{"DB_URL=mysql://127.0.0.1:4242/x"}, filepath.Join(dir, "app.log"), "perfil.json")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	if a.URL != "http://127.0.0.1:"+strconv.Itoa(puerto) {
		t.Fatalf("URL %s", a.URL)
	}
	quiero := strconv.Itoa(puerto) + " mysql://127.0.0.1:4242/x nada nada\n"
	if got := leerCuandoEste(t, filepath.Join(dir, "salida.txt")); got != quiero {
		t.Fatalf("salida %q, quería %q", got, quiero)
	}
	if len(a.Procesos) != 1 || !grupoNuestro(a.Procesos[0]) {
		t.Fatalf("procesos: %+v", a.Procesos)
	}
}

// Un comando que falla corta la espera enseguida y el motivo lleva el final del log.
func TestArrancarAppComandoQueFalla(t *testing.T) {
	viejo := esperaApp
	t.Cleanup(func() { esperaApp = viejo })
	esperaApp = 30 * time.Second
	dir := t.TempDir()
	inicio := time.Now()
	_, err := arrancarApp(context.Background(), RecetaPreview{Comandos: []string{"echo 'falta la variable X' >&2; exit 3"}}, dir, nil, filepath.Join(dir, "app.log"), "perfil.json")
	if err == nil || !strings.Contains(err.Error(), "falta la variable X") || !strings.Contains(err.Error(), "terminó con error") {
		t.Fatalf("motivo: %v", err)
	}
	if time.Since(inicio) > 10*time.Second {
		t.Fatal("esperó el timeout en vez de cortar al fallar el comando")
	}
}

// Una app que nunca responde vence la espera, con el log en el motivo, y sus procesos se bajan.
func TestArrancarAppNoResponde(t *testing.T) {
	viejo := esperaApp
	t.Cleanup(func() { esperaApp = viejo })
	esperaApp = 2 * time.Second
	dir := t.TempDir()
	receta := RecetaPreview{Comandos: []string{"echo $$ > pid.txt; echo compilando; sleep 60"}}
	_, err := arrancarApp(context.Background(), receta, dir, nil, filepath.Join(dir, "app.log"), "perfil.json")
	if err == nil || !strings.Contains(err.Error(), "no respondió") || !strings.Contains(err.Error(), "compilando") {
		t.Fatalf("motivo: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(leerCuandoEste(t, filepath.Join(dir, "pid.txt"))))
	esperarMuerto(t, pid)
}

// `preview bajar` y el vencimiento bajan los procesos de la receta, incluso los de fondo.
func TestBajarYVencer(t *testing.T) {
	servidorEnPuerto(t)
	for _, caso := range []string{"bajar", "vencer"} {
		t.Run(caso, func(t *testing.T) {
			runs, dir := runsDePrueba(t), t.TempDir()
			receta := RecetaPreview{Comandos: []string{"sleep 60 &", "sleep 60"}}
			a, err := arrancarApp(context.Background(), receta, dir, nil, filepath.Join(dir, "app.log"), "perfil.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := savePreview(runs, &Preview{ID: "a", URL: a.URL, Procesos: a.Procesos, Hasta: time.Now().Add(-time.Second)}); err != nil {
				t.Fatal(err)
			}
			if caso == "bajar" {
				StopPreview(runs)
				if LoadPreview(runs) != nil {
					t.Fatal("quedó el registro")
				}
			} else {
				VencerPreview(runs)
				if p := LoadPreview(runs); p == nil || !p.Apagada || len(p.Procesos) != 0 {
					t.Fatalf("no quedó apagada: %+v", p)
				}
			}
			for _, p := range a.Procesos {
				esperarMuerto(t, p.PGID)
			}
		})
	}
}

// El frontend del proyecto es donde corre la receta; sin frontend, el primer repo.
func TestRepoApp(t *testing.T) {
	cl := Cliente{Slug: "acme", Repos: []RepoCliente{{Nombre: "acme-api"}, {Nombre: "acme-web", Front: true}}}
	repos := []Repo{{Name: "acme-api", WT: "/a"}, {Name: "acme-web", WT: "/w"}}
	if r := repoApp(cl, repos); r.Name != "acme-web" {
		t.Fatalf("eligió %s", r.Name)
	}
	cl.Repos[1].Front = false
	if r := repoApp(cl, repos); r.Name != "acme-api" {
		t.Fatalf("sin frontend eligió %s", r.Name)
	}
}

// UX y UI: corren si la spec toca un frontend con rutas y el proyecto declara preview; sin preview
// quedan "sin revisar" (infra) y no frenan; sin frontend no se suman.
func TestCuandoCorrenUXUI(t *testing.T) {
	spec := Spec{Repos: []string{"acme-web"}, Rutas: []string{"/"}}
	con := Config{Home: espacioConPreview(t, "    preview:\n      comandos: [\"npm run dev -- --port {puerto}\"]\n")}
	if !con.touchesUI(spec) {
		t.Fatal("un frontend con rutas toca la UI")
	}
	if _, r, err := con.recetaDe(spec.Repos); err != nil || r.Comandos[0] != "npm run dev -- --port {puerto}" {
		t.Fatalf("con preview hay receta: %v %v", r, err)
	}
	if con.touchesUI(Spec{Repos: []string{"acme-api"}, Rutas: []string{"/"}}) || con.touchesUI(Spec{Repos: []string{"acme-web"}}) {
		t.Fatal("sin frontend o sin rutas no hay UX/UI")
	}
	sin := Config{Home: espacioConPreview(t, ""), Runs: runsDePrueba(t)}
	task := &Task{Dir: t.TempDir()}
	app, revs, err := sin.uiReviewers(context.Background(), task, spec, []Repo{{Name: "acme-web", WT: t.TempDir()}}, 1, nil)
	if app != nil || err != nil || len(revs) != 2 {
		t.Fatalf("sin preview: app=%v err=%v revs=%+v", app, err, revs)
	}
	for _, r := range revs {
		if !strings.HasPrefix(r.setupErr, infraPrefijo) || !strings.Contains(r.setupErr, "no declara preview") {
			t.Fatalf("%s: %q", r.name, r.setupErr)
		}
	}
}

// El revisor entra con el usuario del entorno; sin él, sin iniciar sesión. Nunca hay uno en el código.
func TestAccesoDesdeElEntorno(t *testing.T) {
	t.Setenv("FABRICA_APP_USUARIO", "")
	if !strings.Contains(acceso(), "sin usuario de prueba") {
		t.Fatal(acceso())
	}
	t.Setenv("FABRICA_APP_USUARIO", "revisor@example.com")
	t.Setenv("FABRICA_APP_CLAVE", "k")
	if got := acceso(); !strings.Contains(got, "revisor@example.com") || !strings.Contains(got, "clave k") {
		t.Fatal(got)
	}
}

// probar-app sin proyecto usa el único con preview; con varios, pide uno.
func TestProyectoConPreview(t *testing.T) {
	home := espacioConPreview(t, "    preview:\n      comandos: [\"x\"]\n")
	if p, err := proyectoConPreview(home); err != nil || p != "acme" {
		t.Fatalf("%q %v", p, err)
	}
	if _, err := proyectoConPreview(espacioConPreview(t, "")); err == nil {
		t.Fatal("sin preview no hay proyecto que probar")
	}
}

// La app es código de la rama: no ve los secretos de la fábrica.
func TestEntornoLimpioSinSecretos(t *testing.T) {
	t.Setenv("FABRICA_SMTP_CLAVE", "x")
	t.Setenv("BETTERSTACK_TOKEN", "y")
	env := entornoLimpio("PORT=8190")
	for _, kv := range env {
		if strings.HasPrefix(kv, "FABRICA_") || strings.HasPrefix(kv, "BETTERSTACK_") {
			t.Fatalf("se coló un secreto: %s", kv)
		}
	}
	if !slices.Contains(env, "PORT=8190") {
		t.Fatal("falta lo que se pidió agregar")
	}
}

// La aprobación o cancelar de una tarea no bajan la vista previa de otra; la suya sí.
func TestStopPreviewDeSoloLaSuya(t *testing.T) {
	runs := runsDePrueba(t)
	if err := savePreview(runs, &Preview{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	StopPreviewDe(runs, "b")
	if LoadPreview(runs) == nil {
		t.Fatal("bajó la vista previa de otra tarea")
	}
	StopPreviewDe(runs, "a")
	if LoadPreview(runs) != nil {
		t.Fatal("no bajó la suya")
	}
}

// Un grupo que no existe, o cuyo líder arrancó en otro momento, no es nuestro: no se le manda la señal.
func TestGrupoAjenoNoSeToca(t *testing.T) {
	if grupoNuestro(Proceso{PGID: 999999, Inicio: "x"}) {
		t.Fatal("un grupo inexistente no puede ser nuestro")
	}
	yo := os.Getpid()
	if grupoNuestro(Proceso{PGID: yo, Inicio: "Mon Jan  1 00:00:00 2001"}) {
		t.Fatal("otro inicio: es otro programa con el mismo número")
	}
	if !grupoNuestro(Proceso{PGID: yo, Inicio: inicioDe(yo)}) {
		t.Fatal("mismo número y mismo inicio: es nuestro")
	}
}

// A los 20 min se apaga y queda marcada para levantarla de nuevo; antes, no se toca.
func TestVencerPreview(t *testing.T) {
	runs := runsDePrueba(t)
	_ = savePreview(runs, &Preview{ID: "a", URL: "http://x", Hasta: time.Now().Add(time.Minute)})
	VencerPreview(runs)
	if p := LoadPreview(runs); p == nil || p.Apagada {
		t.Fatalf("se apagó antes de tiempo: %+v", p)
	}
	_ = savePreview(runs, &Preview{ID: "a", URL: "http://x", Hasta: time.Now().Add(-time.Second)})
	VencerPreview(runs)
	if p := LoadPreview(runs); p == nil || !p.Apagada {
		t.Fatalf("no se apagó al vencer, o perdió el registro: %+v", p)
	}
}

// Sin tope (0, con la suscripción) nunca frena; con tope, frena al pasarlo.
func TestPasaTope(t *testing.T) {
	c := Config{Runs: t.TempDir()}
	if c.PasaTope(1000) {
		t.Fatal("sin tope configurado no puede frenar")
	}
	c.BudgetDayUSD = 20
	if !c.PasaTope(21) || c.PasaTope(1) {
		t.Fatal("con tope de 20: 21 frena, 1 no")
	}
}

// Camino completo de la vista previa: la rama en el repo de la spec, el hermano en main, la receta en
// el frontend y el registro con sus procesos para bajarla.
func TestStartPreviewConReceta(t *testing.T) {
	servidorEnPuerto(t)
	origenes, salida := t.TempDir(), filepath.Join(t.TempDir(), "donde.txt")
	for _, n := range []string{"acme-api", "acme-web"} {
		o := filepath.Join(origenes, n)
		for _, args := range [][]string{{"init", "-q", "-b", "main", o}, {"-C", o, "commit", "-q", "--allow-empty", "-m", "base"}} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		}
	}
	home := t.TempDir()
	yml := "proyectos:\n  acme:\n    repos:\n      - nombre: acme-api\n        ruta: " + filepath.Join(origenes, "acme-api") +
		"\n      - nombre: acme-web\n        ruta: " + filepath.Join(origenes, "acme-web") + "\n        frontend: true\n" +
		"    preview:\n      comandos: [\"echo $(basename $PWD) $(ls .. | tr '\\\\n' ' ') > " + salida + "; sleep 60\"]\n"
	if err := os.WriteFile(filepath.Join(home, ArchivoEspacio), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{Home: home, Runs: runsDePrueba(t)}
	task, err := AddTask(c.Runs, "x", "ticket", Pendiente, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := task.WriteJSON("01-spec.json", Spec{Repos: []string{"acme-web"}, Rutas: []string{"/clientes"}}); err != nil {
		t.Fatal(err)
	}
	p := c.StartPreview(context.Background(), task)
	if p.Error != "" || !strings.HasPrefix(p.URL, "http://127.0.0.1:") {
		t.Fatalf("vista previa: %+v", p)
	}
	if got := leerCuandoEste(t, salida); got != "acme-web acme-api acme-web\n" {
		t.Fatalf("la receta corrió en %q", got)
	}
	if rama, _ := git(t.Context(), filepath.Join(task.Dir, "wt", "acme-web"), "branch", "--show-current"); rama != Branch(task.Est.ID) {
		t.Fatalf("el frontend no está en la rama de la tarea: %q", rama)
	}
	guardada := LoadPreview(c.Runs)
	if guardada == nil || guardada.URL != p.URL || len(guardada.Procesos) != 1 || guardada.Hasta.IsZero() {
		t.Fatalf("registro: %+v", guardada)
	}
	StopPreview(c.Runs)
	esperarMuerto(t, guardada.Procesos[0].PGID)
}
