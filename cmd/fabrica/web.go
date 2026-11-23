package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/santtisosa/ticket-factory/internal/fab"
)

// La página es el diagrama de la fábrica. Solo lee runs/, salvo dos cosas de una tarea que espera tu
// aprobación: la aprobación (con su confirmación) y levantar de nuevo su vista previa. Lo demás, en la terminal.
const webAddr = "127.0.0.1:7171"

//go:embed web
var webFS embed.FS

var (
	reID = regexp.MustCompile(`^\d{8}-\d{4}-[0-9a-f]{4}$`)
	// Lo que entrega cada agente. Nunca wt/ (código de los proyectos), next.log ni estado interno.
	reArchivo = regexp.MustCompile(`^(0[0-5]-[a-z]+(-v\d+)?\.(json|md)|ui-v\d+/[A-Za-z0-9-]+(\.png|\.axe\.json|\.aria\.yml)|ui-v\d+/resumen\.json)$`)
	hosts     = map[string]bool{webAddr: true, "localhost:7171": true}
	// Las dos escrituras de la página. La marca X-Fabrica tiene que nombrar la acción.
	reEscritura = regexp.MustCompile(`^/api/(?:tareas/\d{8}-\d{4}-[0-9a-f]{4}/(aprobar|preview)|(abrir))$`)
	// Una aprobación a la vez: un doble click no puede abrir dos PRs.
	aprobarMu sync.Mutex
	// pedidos: el último pedido de aprobación (no seco) por tarea, para el límite de pedidosCada.
	pedidosMu sync.Mutex
	pedidos   = map[string]time.Time{}
	ahora     = time.Now // variable para los tests
)

const aprobarTimeout = 5 * time.Minute

// pedidosCada: la página pide la aprobación de una tarea como mucho una vez en este lapso. El código
// encerrado llega a localhost y podría pedirla en bucle hasta que alguien pulse el diálogo por costumbre.
const pedidosCada = 5 * time.Minute

// pedidoPermitido registra el intento y dice si pasa el límite. Cuentan los intentos, no los éxitos.
func pedidoPermitido(id string) bool {
	pedidosMu.Lock()
	defer pedidosMu.Unlock()
	if ultimo, ok := pedidos[id]; ok && ahora().Sub(ultimo) < pedidosCada {
		return false
	}
	pedidos[id] = ahora()
	return true
}

type tareaWeb struct {
	fab.Estado
	Fuente   string      `json:"fuente"`
	Cliente  string      `json:"cliente"` // de qué proyecto vino
	Texto    string      `json:"texto"`
	Creada   time.Time   `json:"creada"`
	Sensible []string    `json:"sensible"`
	Archivos []string    `json:"archivos"`
	Eventos  []fab.Event `json:"eventos"`
	PRs      []string    `json:"prs,omitempty"` // las URLs de las PRs que abrió la aprobación
}

type estadoWeb struct {
	Ahora      time.Time    `json:"ahora"`
	Corriendo  bool         `json:"corriendo"`
	CadaMin    int          `json:"ingesta_cada_min"`
	Ingesta    *fab.Ingesta `json:"ingesta"`
	GastadoHoy float64      `json:"gastado_hoy"`
	TopeDia    float64      `json:"tope_dia"`
	Tareas     []tareaWeb   `json:"tareas"`
	Preview    *fab.Preview `json:"preview"`
	// Arranque cambia con cada servidor nuevo: la página abierta se recarga y no se queda con el código viejo.
	Arranque string `json:"arranque"`
}

var arranque = time.Now().Format(time.RFC3339Nano)

func web(ctx context.Context, cfg fab.Config, abrir bool) error {
	estaticos, err := fs.Sub(webFS, "web")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(estaticos))
	mux.HandleFunc("GET /api/estado", func(w http.ResponseWriter, _ *http.Request) { verEstado(cfg, w) })
	mux.HandleFunc("GET /api/tareas/{id}/archivos/{nombre...}", func(w http.ResponseWriter, r *http.Request) {
		verArchivo(cfg, w, r)
	})
	mux.HandleFunc("POST /api/tareas/{id}/aprobar", func(w http.ResponseWriter, r *http.Request) { darAprobacion(cfg, w, r) })
	mux.HandleFunc("POST /api/abrir", func(w http.ResponseWriter, r *http.Request) { abrirEnNavegador(cfg, w, r) })
	mux.HandleFunc("POST /api/tareas/{id}/preview", func(w http.ResponseWriter, r *http.Request) { relevantarPreview(cfg, w, r) })
	go fab.VencerPreviews(context.Background(), cfg.Runs)
	url := "http://" + webAddr
	fmt.Println("la fábrica en", url)
	if abrir {
		abrirVentana(url)
	}
	// SIGTERM la baja: antes la señal la atrapaba NotifyContext y nadie la miraba, así que `kill` no la
	// paraba y seguía sirviendo la página del binario viejo. Shutdown espera a una aprobación en curso.
	srv := &http.Server{Addr: webAddr, Handler: soloLocal(mux)}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.WithoutCancel(ctx))
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// abrirVentana: Warp no tiene un panel web, así que la página va en una ventana de Chrome sin
// pestañas ni barra (modo app), para acomodarla al lado de Warp. Sin Chrome, el navegador de siempre.
func abrirVentana(url string) {
	if _, err := os.Stat("/Applications/Google Chrome.app"); err == nil {
		if exec.Command("open", "-na", "Google Chrome", "--args", "--app="+url, "--window-size=960,1000").Start() == nil {
			return
		}
	}
	_ = exec.Command("open", url).Start()
}

// soloLocal: el Host corta el DNS rebinding. Las únicas escrituras son la aprobación y la vista previa, y
// solo desde la página:
// la marca X-Fabrica obliga a otro sitio a un preflight de CORS que acá nadie contesta, y el Origin
// tiene que ser el de la página.
func soloLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			http.Error(w, "host no permitido", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			m := reEscritura.FindStringSubmatch(r.URL.Path)
			if r.Method != http.MethodPost || m == nil {
				http.Error(w, "la página solo lee, da la aprobación y levanta la vista previa: lo demás va por la terminal", http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("X-Fabrica") != m[1]+m[2] || !hosts[strings.TrimPrefix(r.Header.Get("Origin"), "http://")] {
				http.Error(w, "esto sale solo de la página de la fábrica", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func verEstado(cfg fab.Config, w http.ResponseWriter) {
	ts, err := fab.LoadTasks(cfg.Runs)
	if err != nil {
		http.Error(w, "no se pudo leer runs/: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out := estadoWeb{
		Ahora: time.Now(), Corriendo: corriendo(cfg.Runs), CadaMin: int(ingestEvery / time.Minute),
		Ingesta: fab.LoadIngesta(cfg.Runs), GastadoHoy: fab.SpentToday(cfg.Runs), TopeDia: cfg.BudgetDayUSD,
		Tareas: make([]tareaWeb, 0, len(ts)), Preview: fab.LoadPreview(cfg.Runs), Arranque: arranque,
	}
	for _, t := range ts {
		var e fab.Entrada
		if b, err := os.ReadFile(filepath.Join(t.Dir, "00-entrada.json")); err == nil {
			_ = json.Unmarshal(b, &e)
		}
		var prs map[string]string
		_ = t.ReadJSON(fab.PRsArchivo, &prs)
		urls := make([]string, 0, len(prs))
		for _, u := range prs {
			if strings.HasPrefix(u, "https://github.com/") {
				urls = append(urls, u)
			}
		}
		sort.Strings(urls)
		var spec struct {
			Repos []string `json:"repos"`
		}
		_ = t.ReadJSON("01-spec.json", &spec)
		out.Tareas = append(out.Tareas, tareaWeb{
			Estado: t.Est, Fuente: e.Fuente, Cliente: e.Cliente, Texto: e.Texto, Creada: e.Creada, Sensible: e.Sensible,
			Archivos: archivos(t.Dir), Eventos: t.LoadEvents(), PRs: urls,
		})
	}
	escribirJSON(w, out)
}

// corriendo mira run.pid: lo escribe fabrica run al arrancar y lo borra al salir.
func corriendo(runs string) bool {
	b, err := os.ReadFile(filepath.Join(runs, ".cache", "run.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err == nil && pid > 0 && syscall.Kill(pid, 0) == nil
}

func archivos(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() && rel != "." && !strings.HasPrefix(rel, "ui-v") {
			return filepath.SkipDir // wt/ y lo demás no se recorre
		}
		if !d.IsDir() && reArchivo.MatchString(rel) {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func verArchivo(cfg fab.Config, w http.ResponseWriter, r *http.Request) {
	id, nombre := r.PathValue("id"), r.PathValue("nombre")
	if !reID.MatchString(id) || !reArchivo.MatchString(nombre) {
		http.Error(w, "archivo no permitido", http.StatusNotFound)
		return
	}
	// os.Root no deja salir de la carpeta de la tarea, ni por un symlink.
	root, err := os.OpenRoot(filepath.Join(cfg.Runs, id))
	if err != nil {
		http.Error(w, "no existe la tarea", http.StatusNotFound)
		return
	}
	defer root.Close()
	f, err := root.Open(nombre)
	if err != nil {
		http.Error(w, "no existe el archivo", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, "no existe el archivo", http.StatusNotFound)
		return
	}
	if strings.HasSuffix(nombre, ".md") || strings.HasSuffix(nombre, ".yml") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	http.ServeContent(w, r, nombre, st.ModTime(), f)
}

func escribirJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "web:", err)
	}
}

type respuestaAprobar struct {
	Lineas []string `json:"lineas"`
	Error  string   `json:"error,omitempty"`
}

// darAprobacion corre la misma aprobación que la terminal. ?seco=1 solo muestra lo que haría. Sin seco,
// el botón solo pide la aprobación: publica únicamente si una persona la confirma por el canal seguro
// (diálogo del sistema o terminal, internal/fab/confirmar.go), nunca por esta petición.
func darAprobacion(cfg fab.Config, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !reID.MatchString(id) {
		http.Error(w, "tarea no válida", http.StatusNotFound)
		return
	}
	if !aprobarMu.TryLock() {
		w.WriteHeader(http.StatusConflict)
		escribirJSON(w, respuestaAprobar{Error: "ya hay una aprobación en curso"})
		return
	}
	defer aprobarMu.Unlock()
	seco := r.URL.Query().Get("seco") == "1"
	if !seco && !pedidoPermitido(id) {
		w.WriteHeader(http.StatusTooManyRequests)
		escribirJSON(w, respuestaAprobar{Error: fmt.Sprintf("ya se pidió la aprobación de %s hace menos de %v; espere o use `fabrica aprobar %s` en la terminal", id, pedidosCada, id)})
		return
	}
	// Que el navegador se cierre no corta un push a mitad.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), aprobarTimeout)
	defer cancel()
	lineas, err := cfg.Aprobar(ctx, id, seco, true)
	fmt.Printf("aprobación desde la página %s (seco=%v): %d pasos, err=%v\n", id, seco, len(lineas), err)
	res := respuestaAprobar{Lineas: lineas}
	if err != nil {
		res.Error = err.Error()
		w.WriteHeader(http.StatusConflict)
	}
	escribirJSON(w, res)
}

// relevantarPreview: el botón de la página cuando la vista previa se apagó o no levantó. Contesta
// enseguida y la levanta aparte (tarda un minuto o más); la página ve el avance en /api/estado.
func relevantarPreview(cfg fab.Config, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !reID.MatchString(id) {
		http.Error(w, "tarea no válida", http.StatusNotFound)
		return
	}
	// El mismo límite que la aprobación, con su propia cuenta (M1): el código encerrado llega a
	// localhost y podría relevantar la vista previa en bucle. Cuentan los intentos, no los éxitos.
	if !pedidoPermitido("preview:" + id) {
		w.WriteHeader(http.StatusTooManyRequests)
		escribirJSON(w, respuestaAprobar{Error: fmt.Sprintf("ya se pidió la vista previa de %s hace menos de %v; espere o use `fabrica preview %s` en la terminal", id, pedidosCada, id)})
		return
	}
	if err := fab.PuedePreview(cfg.Runs, id); err != nil {
		w.WriteHeader(http.StatusConflict)
		escribirJSON(w, respuestaAprobar{Error: err.Error()})
		return
	}
	go func() {
		if _, err := cfg.PreviewDe(context.Background(), id); err != nil {
			fmt.Fprintln(os.Stderr, "vista previa", id+":", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
	escribirJSON(w, respuestaAprobar{Lineas: []string{"levantando"}})
}

// abrirEnNavegador: la ventana de la fábrica es Chrome en modo app, pero los links (la PR, la vista
// previa) van al navegador por defecto, donde está la sesión de GitHub. Solo la URL de una PR que
// guardó la fábrica o una página de la vista previa registrada (fab.URLAbrible): la página no abre
// cualquier cosa, aunque el pedido venga de código encerrado que llega a localhost.
func abrirEnNavegador(cfg fab.Config, w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !urlPermitida(in.URL) || !fab.URLAbrible(cfg.Runs, in.URL) {
		http.Error(w, "link no permitido", http.StatusBadRequest)
		return
	}
	if err := exec.Command("open", in.URL).Start(); err != nil {
		http.Error(w, "no se pudo abrir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func urlPermitida(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.User != nil {
		return false
	}
	switch {
	case u.Scheme == "https" && u.Host == "github.com":
		return true
	case u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "":
		return true // la vista previa: http://127.0.0.1:<puerto>
	}
	return false
}
