package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santtisosa/ticket-factory/internal/fab"
)

// La página sirve lo que entrega cada agente y nada más: ni el worktree con código de clientes,
// ni rutas que salgan de la tarea.
func TestArchivoSoloLoQueEntreganLosAgentes(t *testing.T) {
	cases := map[string]bool{
		"01-spec.md": true, "02-build-v1.json": true, "03-seguridad.json": true, "04-approver-v2.json": true,
		"ui-v1/inicio-escritorio.png": true, "ui-v1/Clientes-ABC1-celular.png": true, "ui-v1/resumen.json": true,
		"wt/demo-backend/main.go": false, "next.log": false, "estado.json": false, "../x/01-spec.md": false,
		"ui-v1/../../otra/01-spec.md": false, "events.jsonl": false,
	}
	for nombre, quiere := range cases {
		if got := reArchivo.MatchString(nombre); got != quiere {
			t.Errorf("%q: permitido=%v, quería %v", nombre, got, quiere)
		}
	}
}

func TestSoloLocal(t *testing.T) {
	ok := soloLocal(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	cases := []struct {
		nombre, metodo, host, origin, marca string
		quiere                              int
	}{
		{"leer desde la página", "GET", webAddr, "", "", http.StatusTeapot},
		{"DNS rebinding", "GET", "malo.com:7171", "", "", http.StatusForbidden},
		{"post desde otro sitio", "POST", webAddr, "https://malo.com", "1", http.StatusMethodNotAllowed},
		// Hasta la página propia: aceptar y descartar van por la terminal.
		{"post desde la página", "POST", webAddr, "http://" + webAddr, "aprobar", http.StatusMethodNotAllowed},
		// La aprobación es la única escritura, y solo desde la página con su marca.
		{"aprobación desde la página", "POST /aprobar", webAddr, "http://" + webAddr, "aprobar", http.StatusTeapot},
		{"aprobación desde otro sitio", "POST /aprobar", webAddr, "https://malo.com", "aprobar", http.StatusForbidden},
		{"aprobación sin Origin", "POST /aprobar", webAddr, "", "aprobar", http.StatusForbidden},
		{"aprobación sin la marca", "POST /aprobar", webAddr, "http://" + webAddr, "", http.StatusForbidden},
		{"aprobación con otra marca", "POST /aprobar", webAddr, "http://" + webAddr, "1", http.StatusForbidden},
		// La vista previa, con su propia marca: la de la aprobación no le sirve, ni al revés.
		{"preview desde la página", "POST /preview", webAddr, "http://" + webAddr, "preview", http.StatusTeapot},
		{"preview con la marca de la aprobación", "POST /preview", webAddr, "http://" + webAddr, "aprobar", http.StatusForbidden},
		{"aprobación con la marca de preview", "POST /aprobar", webAddr, "http://" + webAddr, "preview", http.StatusForbidden},
		{"preview desde otro sitio", "POST /preview", webAddr, "https://malo.com", "preview", http.StatusForbidden},
		{"abrir desde la página", "POST abrir", webAddr, "http://" + webAddr, "abrir", http.StatusTeapot},
		{"abrir con otra marca", "POST abrir", webAddr, "http://" + webAddr, "aprobar", http.StatusForbidden},
		{"abrir desde otro sitio", "POST abrir", webAddr, "https://malo.com", "abrir", http.StatusForbidden},
	}
	for _, c := range cases {
		metodo, ruta := c.metodo, "/api/tareas"
		if accion, ok := strings.CutPrefix(metodo, "POST /"); ok {
			metodo, ruta = "POST", "/api/tareas/20261031-1032-a7f3/"+accion
		}
		if metodo == "POST abrir" {
			metodo, ruta = "POST", "/api/abrir"
		}
		r := httptest.NewRequest(metodo, ruta, nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.marca != "" {
			r.Header.Set("X-Fabrica", c.marca)
		}
		w := httptest.NewRecorder()
		ok.ServeHTTP(w, r)
		if w.Code != c.quiere {
			t.Errorf("%s: %d, quería %d", c.nombre, w.Code, c.quiere)
		}
	}
}

func TestVerArchivoNoSaleDeLaTarea(t *testing.T) {
	runs := t.TempDir()
	id := "20261030-1200-abcd"
	if err := os.MkdirAll(filepath.Join(runs, id), 0o755); err != nil {
		t.Fatal(err)
	}
	secreto := filepath.Join(runs, "secreto.json")
	_ = os.WriteFile(secreto, []byte(`{"clave":"x"}`), 0o644)
	_ = os.Symlink(secreto, filepath.Join(runs, id, "03-seguridad.json"))
	_ = os.WriteFile(filepath.Join(runs, id, "01-spec.md"), []byte("# spec"), 0o644)
	cfg := fab.Config{Runs: runs}

	get := func(nombre string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/tareas/"+id+"/archivos/"+nombre, nil)
		r.SetPathValue("id", id)
		r.SetPathValue("nombre", nombre)
		w := httptest.NewRecorder()
		verArchivo(cfg, w, r)
		return w
	}
	if w := get("01-spec.md"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "# spec") {
		t.Errorf("la spec: %d %q", w.Code, w.Body.String())
	}
	if w := get("03-seguridad.json"); w.Code == http.StatusOK {
		t.Errorf("un symlink fuera de la tarea no se sirve: %d %q", w.Code, w.Body.String())
	}
}

// La página abre en el navegador por defecto solo GitHub y la vista previa local.
func TestURLPermitida(t *testing.T) {
	cases := map[string]bool{
		"https://github.com/ejemplo/app/pull/6":  true,
		"http://127.0.0.1:53412/catalogo":        true,
		"http://127.0.0.1:8080":                  true,
		"http://127.0.0.1/":                      false,
		"https://127.0.0.1:8080":                 false,
		"http://ejemplo.localhost:3290/catalogo": false,
		"http://localhost:3290":                  false,
		"http://127.0.0.1.malo.com:80":           false,
		"https://github.com.malo.com/x":          false,
		"http://github.com/x":                    false,
		"file:///etc/passwd":                     false,
		"https://user:pass@github.com/x":         false,
		"javascript:alert(1)":                    false,
		"https://malo.com":                       false,
	}
	for u, quiere := range cases {
		if got := urlPermitida(u); got != quiere {
			t.Errorf("%q: %v, quería %v", u, got, quiere)
		}
	}
}

func TestBinarioCambio(t *testing.T) {
	f := filepath.Join(t.TempDir(), "factory")
	if err := os.WriteFile(f, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	inicio := time.Now().Add(-time.Minute)
	if err := os.Chtimes(f, inicio, inicio); err != nil {
		t.Fatal(err)
	}
	if binarioCambio(f, inicio) {
		t.Fatal("mismo binario: no cambió")
	}
	if err := os.WriteFile(f, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !binarioCambio(f, inicio) {
		t.Fatal("binario más nuevo: cambió")
	}
	if binarioCambio("", inicio) {
		t.Fatal("sin ruta: no")
	}
}

// La página pide la aprobación de una tarea como mucho una vez cada pedidosCada; cuentan los intentos.
func TestPedidoPermitido(t *testing.T) {
	reloj := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	viejo := ahora
	t.Cleanup(func() { ahora = viejo })
	ahora = func() time.Time { return reloj }
	if !pedidoPermitido("t-limite") {
		t.Fatal("el primero pasa")
	}
	reloj = reloj.Add(pedidosCada - time.Second)
	if pedidoPermitido("t-limite") {
		t.Fatal("el segundo antes de 5 min no pasa")
	}
	if !pedidoPermitido("t-otra") {
		t.Fatal("el límite es por tarea")
	}
	reloj = reloj.Add(time.Second) // 5 min desde el primer pedido, aunque el diálogo haya dicho que no
	if !pedidoPermitido("t-limite") {
		t.Fatal("pasados 5 min se puede volver a pedir")
	}
}

// M1: relevantar la vista previa tiene el mismo límite que la aprobación, con su propia cuenta.
func TestRelevantarPreviewConLimite(t *testing.T) {
	reloj := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)
	viejo := ahora
	t.Cleanup(func() { ahora = viejo })
	ahora = func() time.Time { return reloj }
	runs := t.TempDir()
	cfg := fab.Config{Runs: runs}
	pedir := func() int {
		r := httptest.NewRequest("POST", "/api/tareas/20261122-1100-aaaa/preview", nil)
		r.SetPathValue("id", "20261122-1100-aaaa")
		w := httptest.NewRecorder()
		relevantarPreview(cfg, w, r)
		return w.Code
	}
	if c := pedir(); c == http.StatusTooManyRequests {
		t.Fatal("el primer pedido pasa el límite")
	}
	if c := pedir(); c != http.StatusTooManyRequests {
		t.Fatalf("el segundo pedido antes de %v: %d", pedidosCada, c)
	}
	if !pedidoPermitido("20261122-1100-aaaa") {
		t.Fatal("la vista previa no gasta el turno de la aprobación")
	}
}

// M1: /api/abrir abre solo la URL de una PR guardada o una página de la vista previa registrada.
func TestAbrirSoloLoRegistrado(t *testing.T) {
	runs := t.TempDir()
	tarea, err := fab.AddTask(runs, "x", "prompt", fab.PRAbierto, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := tarea.WriteJSON(fab.PRsArchivo, map[string]string{"web": "https://github.com/o/web/pull/7"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runs, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, ".cache", "preview.json"), []byte(`{"url":"http://127.0.0.1:41234"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"https://github.com/o/web/pull/7":        true,
		"http://127.0.0.1:41234/clientes":        true,
		"https://github.com/o/web/pull/8":        false,
		"https://github.com/o/otro/pull/7":       false,
		"http://127.0.0.1:7070/api/estado":       false,
		"http://127.0.0.1:41235/":                false,
		"https://x:y@github.com/o/web/pull/7":    false,
		"https://github.com/o/web/pull/7?x=evil": false,
	}
	for u, quiere := range cases {
		if got := urlPermitida(u) && fab.URLAbrible(runs, u); got != quiere {
			t.Errorf("%q: %v, quería %v", u, got, quiere)
		}
	}
}
