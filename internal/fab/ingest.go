package fab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const fetchTimeout = 2 * time.Minute

// Propuesta: lo que entra por un log o por un ticket de un autor no autorizado. Nunca arranca un
// build solo: el operador la pasa a PENDIENTE con `fabrica aceptar <id>`.
const Propuesta = "PROPUESTA"

// Item es algo que trae una fuente, antes de ser tarea.
type Item struct {
	Clave  string // única por fuente: dedup entre lecturas
	Fuente string // tarjeta | ticket | log
	Texto  string
	Listo  bool // true → PENDIENTE; false → PROPUESTA
	// Servicio: de dónde salió (nombre del servicio o del sistema); se traduce a proyecto.
	Servicio string
	Veces    int // ocurrencias en la ventana de la fuente (logs); sube la prioridad
	// Origen: la referencia del ticket en su sistema (owner/repo#n de un issue). La usan el
	// seguimiento por etiquetas y la notificación por comentario.
	Origen string
	// Cliente: el proyecto que declara la fuente; gana sobre el deducido por Servicio.
	Cliente string
}

// PrioridadDe: un error de producción va antes que un pedido, y este antes que una sugerencia; lo
// que se repite y lo sensible suben. El operador la cambia con `fabrica prioridad`.
func PrioridadDe(fuente string, veces int, sensible []string) int {
	base := map[string]int{"log": 20, "prompt": 15, "ticket": 10, "tarjeta": 10}[fuente]
	return base + min(veces, 50)/5 + 5*len(sensible)
}

// clientesDe: runs/ es siempre <espacio>/runs (config.go).
func clientesDe(runs string) []Cliente {
	return Clientes(filepath.Dir(runs))
}

// Source lee una fuente externa, siempre de solo lectura. Si le falta configuración devuelve
// ErrNoConfig y la fábrica sigue con las demás.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]Item, error)
}

type errNoConfig struct{ falta string }

func (e errNoConfig) Error() string { return "sin configurar: falta " + e.falta }

// FuenteEstado es cómo le fue a una fuente en la última lectura, para la página.
type FuenteEstado struct {
	Nombre  string `json:"nombre"`
	OK      bool   `json:"ok"`
	SinConf bool   `json:"sin_config"` // le falta configuración: está apagada, no rota
	Detalle string `json:"detalle"`
	Nuevas  int    `json:"nuevas"`
	Leidas  int    `json:"leidas"`
}

// Ingesta es runs/.cache/ingesta.json: la última lectura de las fuentes.
type Ingesta struct {
	TS      time.Time      `json:"ts"`
	Fuentes []FuenteEstado `json:"fuentes"`
}

func ingestaPath(runs string) string { return filepath.Join(runs, ".cache", "ingesta.json") }

// LoadIngesta devuelve la última lectura, o nil si todavía no hubo ninguna.
func LoadIngesta(runs string) *Ingesta {
	b, err := os.ReadFile(ingestaPath(runs))
	if err != nil {
		return nil
	}
	var in Ingesta
	if json.Unmarshal(b, &in) != nil {
		return nil
	}
	return &in
}

// Ingest lee todas las fuentes y carga lo nuevo. Devuelve un renglón por fuente para el log y deja
// el estado de cada una en ingesta.json, para que la página muestre si los tickets están llegando.
func Ingest(ctx context.Context, runs string, srcs []Source) []string {
	in := Ingesta{TS: time.Now()}
	var report []string
	for _, s := range srcs {
		fe := ingestOne(ctx, runs, s)
		in.Fuentes = append(in.Fuentes, fe)
		report = append(report, s.Name()+": "+fe.Detalle)
	}
	if err := saveIngesta(runs, in); err != nil {
		report = append(report, "no se pudo guardar el estado de las fuentes: "+err.Error())
	}
	return report
}

func ingestOne(ctx context.Context, runs string, s Source) FuenteEstado {
	fe := FuenteEstado{Nombre: s.Name()}
	// Con su propio tope: una fuente lenta no puede trabar la cola.
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	items, err := s.Fetch(fctx)
	cancel()
	if err != nil {
		var nc errNoConfig
		fe.SinConf = errors.As(err, &nc)
		fe.Detalle = err.Error()
		return fe
	}
	fe.Leidas = len(items)
	for _, it := range items {
		if visto(runs, it.Clave) {
			continue
		}
		estado := Propuesta
		if it.Listo {
			estado = Pendiente
		}
		cliente := it.Cliente
		if cliente == "" {
			cliente = ClienteDeServicio(clientesDe(runs), it.Servicio)
		}
		texto := Mask(it.Texto)
		t, err := addTaskDesde(runs, texto, it.Fuente, estado, cliente, it.Origen)
		if err == nil {
			err = SetPrioridad(runs, t.Est.ID, PrioridadDe(it.Fuente, it.Veces, Sensible(texto)))
		}
		if err != nil {
			fe.Detalle = err.Error()
			return fe
		}
		if err := marcarVisto(runs, it.Clave); err != nil {
			fe.Detalle = "no se pudo guardar lo ya visto (se va a duplicar): " + err.Error()
			return fe
		}
		fe.Nuevas++
	}
	fe.OK = true
	fe.Detalle = fmt.Sprintf("%d nuevas de %d", fe.Nuevas, fe.Leidas)
	return fe
}

func saveIngesta(runs string, in Ingesta) error {
	b, err := json.MarshalIndent(in, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ingestaPath(runs)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ingestaPath(runs), b, 0o644)
}

// visto y marcarVisto: la deduplicación por clave de la fuente vive en factory.db.
func visto(runs, clave string) bool {
	var n int
	_ = mustDB(runs).QueryRow(`SELECT COUNT(*) FROM vistos WHERE clave=?`, clave).Scan(&n)
	return n > 0
}

func marcarVisto(runs, clave string) error {
	_, err := mustDB(runs).Exec(`INSERT OR IGNORE INTO vistos(clave,ts) VALUES(?,?)`, clave, time.Now().UnixNano())
	return err
}

// Mask tapa datos personales antes de que el texto llegue a un modelo.
var maskers = []struct {
	re  *regexp.Regexp
	rep string
}{
	// Primero lo que trae credenciales adentro: un DSN o una URL con usuario y contraseña.
	{regexp.MustCompile(`\b([a-z][a-z0-9+.-]*://)[^:/@\s]+:[^@\s]+@`), "$1<oculto>@"},
	{regexp.MustCompile(`\b[\w.-]+:[^@\s:]+@(tcp|unix)\(`), "<oculto>@$1("}, // DSN de MySQL en Go
	{regexp.MustCompile(`\beyJ[\w-]+\.[\w-]+\.[\w-]+`), "<jwt>"},
	{regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`), "<email>"},
	{regexp.MustCompile(`(?i)\bbearer\s+[\w.~+/-]+=*`), "Bearer <oculto>"},
	// Sin \b adelante: access_token, DB_PASSWORD y client_secret también son claves. El valor puede venir
	// entre comillas y con espacios.
	{regexp.MustCompile(`(?i)[\w-]*?(token|password|passwd|contraseña|clave|secret|api[_-]?key)["']?\s*[=:]\s*("[^"]*"|'[^']*'|[^\s"',;]+)`), "$1=<oculto>"},
	// Cédula uruguaya con sus puntos o su guion: un monto sin separadores (12345678) no se tapa.
	{regexp.MustCompile(`\b\d\.\d{3}\.\d{3}-?\d\b|\b\d{7}-\d\b`), "<documento>"},
	{regexp.MustCompile(`\b\d{12}\b`), "<rut>"},
	// Celular uruguayo con su 0 o su +598: un número suelto que empieza con 9 (un monto, un id) queda.
	{regexp.MustCompile(`(\+598[\s-]?9\d|\b09\d)[\s-]?\d{3}[\s-]?\d{3}\b`), "<telefono>"},
}

func Mask(s string) string {
	for _, m := range maskers {
		s = m.re.ReplaceAllString(s, m.rep)
	}
	return s
}

// Sensible marca por regla, no por LLM, si la tarea toca plata, auth o datos de clientes.
// En español y en inglés (los logs llegan en inglés), por palabra entera: "activa" no es "iva".
// "cliente" no cuenta: es demasiado común en los pedidos.
var sensibles = map[string]*regexp.Regexp{
	"plata": regexp.MustCompile(`(?i)\b(factur\w*|cobr\w*|pagos?|pagar|pagad[oa]s?|montos?|precios?|asientos?|contab\w*|saldos?|monedas?|centavos?|impuestos?|iva|invoices?|payments?|prices?|amounts?|balances?|tax(es)?|currenc(y|ies)|refunds?)\b`),
	"auth":  regexp.MustCompile(`(?i)\b(login|logout|sesi[oó]n|sesiones|sessions?|tokens?|contraseñas?|passwords?|permisos?|permissions?|roles?|auth\w*|csrf|cookies?)\b`),
	"datos": regexp.MustCompile(`(?i)\b(tenants?|privacidad|privacy|datos personales|personal data|pii|backups?|respaldos?|migraci[oó]n|migraciones|migrations?)\b`),
}

func Sensible(texto string) []string {
	var out []string
	for _, k := range []string{"plata", "auth", "datos"} {
		if sensibles[k].MatchString(texto) {
			out = append(out, k)
		}
	}
	return out
}
