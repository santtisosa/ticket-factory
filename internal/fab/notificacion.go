package fab

// Notificación a quien pidió el cambio. Cuando una tarea llega a producción, la línea redacta el
// mensaje (agents/notificacion.md) y lo deja en 09-notificacion.json. `fabrica notificar <id>` lo
// muestra con su destinatario; `--enviar` lo envía por el canal de fabrica.yaml (smtp, comentario o
// exec), una sola vez y solo por orden del operador.

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	notificacionArchivo = "09-notificacion.json"
	// notificacionPendiente: la tarea llegó a producción y falta redactar su notificación. La marca la
	// pone la transición y la redacción corre en la vuelta posterior al merge: es una llamada a un
	// modelo y no puede frenar el resto. Sin marca no hay redacción.
	notificacionPendiente = "09-notificacion-pendiente"
	execNotificarTimeout  = 2 * time.Minute
	smtpPuertoPorDefecto  = "587"
	asuntoNotificacion    = "Su solicitud ya está disponible"
)

// Borrador es el mensaje de una tarea y, si salió, cuándo y a quién.
type Borrador struct {
	Mensaje string     `json:"mensaje"`
	Canal   string     `json:"canal,omitempty"`
	Para    string     `json:"para,omitempty"`
	Enviado *time.Time `json:"enviado,omitempty"`
}

// notificable: hay canal configurado, la tarea no es un incidente de los logs y, si el canal es
// comentario, vino de un issue.
func notificable(canal string, t *Task) bool {
	var e Entrada
	if canal == "" || t.ReadJSON("00-entrada.json", &e) != nil || e.Fuente == "log" {
		return false
	}
	if canal == "comentario" {
		_, _, ok := issueDe(t)
		return ok
	}
	return true
}

// marcarNotificacionPendiente: la tarea llegó a producción; si corresponde, queda para redactar.
func marcarNotificacionPendiente(t *Task) {
	e, err := CargarEspacio(filepath.Dir(t.runs))
	if err != nil || !notificable(e.Notificacion.Canal, t) {
		return
	}
	if err := os.WriteFile(filepath.Join(t.Dir, notificacionPendiente), nil, 0o644); err != nil {
		t.Log("09-notificacion", "falla", "no se pudo marcar la notificación pendiente: "+err.Error(), 0)
	}
}

// RedactarNotificacionesPendientes redacta las notificaciones marcadas y devuelve un renglón por tarea.
func (c Config) RedactarNotificacionesPendientes(ctx context.Context) []string {
	ts, err := LoadTasks(c.Runs)
	if err != nil {
		return []string{"notificación: " + err.Error()}
	}
	var log []string
	for _, t := range ts {
		marca := filepath.Join(t.Dir, notificacionPendiente)
		if _, err := os.Stat(marca); err != nil || t.Est.Estado != Integrada {
			continue
		}
		c.redactarNotificacion(ctx, t)
		var b Borrador
		if t.ReadJSON(notificacionArchivo, &b) == nil && b.Mensaje != "" {
			_ = os.Remove(marca)
			log = append(log, fmt.Sprintf("notificación %s: redactada, `fabrica notificar %s` la muestra", t.Est.ID, t.Est.ID))
		}
	}
	return log
}

// ultimoBuild: lo que entregó el Builder en su última vuelta.
func ultimoBuild(t *Task) Build {
	var b Build
	vs, _ := filepath.Glob(filepath.Join(t.Dir, "02-build-v*.json"))
	sort.Strings(vs)
	if len(vs) > 0 {
		_ = t.ReadJSON(filepath.Base(vs[len(vs)-1]), &b)
	}
	return b
}

// redactarNotificacion escribe el borrador si todavía no existe. Si el agente falla, queda sin
// borrador: `fabrica notificar` lo vuelve a intentar.
func (c Config) redactarNotificacion(ctx context.Context, t *Task) {
	var previo Borrador
	if t.ReadJSON(notificacionArchivo, &previo) == nil && previo.Mensaje != "" {
		return
	}
	var e Entrada
	_ = t.ReadJSON("00-entrada.json", &e)
	var spec Spec
	_ = t.ReadJSON("01-spec.json", &spec)
	prompt := reglaDato + "\n## Lo que se pidió\n" + noConfiable("entrada_no_confiable", e.Texto) +
		"\n## La spec de lo que se hizo\n" + noConfiable("spec", spec.SpecMD) +
		"\n## Lo que se construyó (resumen del Builder)\n" + noConfiable("entrada_no_confiable", ultimoBuild(t).Resumen) +
		"\n\nEscribe el mensaje según el esquema."
	var out struct {
		Mensaje string `json:"mensaje"`
	}
	call := Call{Dir: c.dirAgentes(), Agent: "notificacion", Schema: schema("notificacion.json"), AllowedTools: []string{"StructuredOutput"},
		Prompt: prompt, BudgetUSD: c.BudgetCallUSD}
	if err := c.expert(ctx, t, "09-notificacion", call, &out); err != nil || strings.TrimSpace(out.Mensaje) == "" {
		t.Log("09-notificacion", "falla", fmt.Sprintf("no se pudo redactar la notificación: %v", err), 0)
		return
	}
	mensaje, quitado := limpiarMensaje(out.Mensaje)
	if quitado {
		t.Log("09-notificacion", "falla", "el borrador traía enlaces, correos o teléfonos: se quitaron (pudieron venir del ticket)", 0)
	}
	_ = t.WriteJSON(notificacionArchivo, Borrador{Mensaje: mensaje})
	t.Log("09-notificacion", "ok", "notificación redactada: `fabrica notificar "+t.Est.ID+"`", 0)
}

// proyectoDe: el proyecto de la entrada o, si la fuente no lo declaró, el del primer repo de la spec.
func proyectoDe(t *Task, cliente string, cs []Cliente) string {
	if cliente != "" {
		return cliente
	}
	var spec Spec
	if t.ReadJSON("01-spec.json", &spec) != nil || len(spec.Repos) == 0 {
		return ""
	}
	_, slug, _ := buscarRepo(cs, spec.Repos[0])
	return slug
}

// destino es a quién va la notificación según el canal.
type destino struct {
	para         string // correo (smtp) o valor de FABRICA_PARA (exec)
	repo, numero string // issue de origen (comentario)
}

func (d destino) describir(canal string) string {
	switch {
	case canal == "comentario":
		return "comentario en " + d.repo + "#" + d.numero
	case d.para != "":
		return d.para
	case canal == "smtp":
		return "(sin destinatario: --para <correo> o proyectos.<p>.notificacion.para)"
	}
	return "(sin destinatario)"
}

// Notificar es `fabrica notificar <id> [--enviar] [--para <correo>] [--mensaje "<texto>"]`.
func (c Config) Notificar(ctx context.Context, id string, enviar bool, para, mensaje string) ([]string, error) {
	t, err := findTask(c.Runs, id)
	if err != nil {
		return nil, err
	}
	esp, err := CargarEspacio(c.Home)
	if err != nil {
		return nil, err
	}
	n := esp.Notificacion
	if n.Canal == "" {
		return nil, errors.New("notificacion.canal no está configurado en " + ArchivoEspacio)
	}
	var e Entrada
	_ = t.ReadJSON("00-entrada.json", &e)
	e.Cliente = proyectoDe(t, e.Cliente, Clientes(c.Home))
	d := destino{para: cmp.Or(para, esp.Proyectos[e.Cliente].Notificacion.Para)}
	if n.Canal == "comentario" {
		var ok bool
		if d.repo, d.numero, ok = issueDe(t); !ok {
			return nil, fmt.Errorf("%s no vino de github-issues: el canal comentario no tiene dónde escribir", id)
		}
	}
	var b Borrador
	if t.ReadJSON(notificacionArchivo, &b) != nil || b.Mensaje == "" {
		c.redactarNotificacion(ctx, t)
		_ = t.ReadJSON(notificacionArchivo, &b)
	}
	if strings.TrimSpace(mensaje) != "" {
		b.Mensaje = strings.TrimSpace(mensaje)
	}
	lines := []string{
		fmt.Sprintf("tarea %s · %s", id, t.Est.Estado),
		"canal: " + n.Canal,
		"para: " + d.describir(n.Canal),
		"", "---- borrador: texto de un modelo a partir del ticket; revisarlo antes de enviar ----",
		b.Mensaje,
		"---- fin del borrador ----",
	}
	if b.Enviado != nil {
		lines = append(lines, "", "ya se envió el "+b.Enviado.Local().Format("02/01 15:04")+" a "+b.Para)
	}
	if !enviar {
		return lines, nil
	}
	switch {
	case b.Enviado != nil:
		return lines, errors.New("esta notificación ya se envió")
	case t.Est.Estado != Integrada:
		return lines, fmt.Errorf("%s está en %s: la notificación sale cuando la tarea está en producción", id, t.Est.Estado)
	case n.Canal == "smtp" && d.para == "":
		return lines, errors.New("falta el destinatario: --para <correo> o proyectos." + cmp.Or(e.Cliente, "<p>") + ".notificacion.para")
	case b.Mensaje == "":
		return lines, errors.New("no hay mensaje: no se pudo redactar; se puede indicar con --mensaje")
	}
	if err := enviarNotificacion(ctx, esp, t, d, b.Mensaje); err != nil {
		return lines, fmt.Errorf("no se envió la notificación (%s): %w", n.Canal, err)
	}
	ahora := time.Now()
	b.Canal, b.Para, b.Enviado = n.Canal, d.describir(n.Canal), &ahora
	if err := t.WriteJSON(notificacionArchivo, b); err != nil {
		lines = append(lines, "ATENCIÓN: se envió, pero no quedó registrado en "+notificacionArchivo+" ("+err.Error()+"): no volver a enviar")
	}
	t.Log("09-notificacion", "ok", "notificación enviada a "+b.Para, 0)
	return append(lines, "", "enviada a "+b.Para), nil
}

func enviarNotificacion(ctx context.Context, esp Espacio, t *Task, d destino, msg string) error {
	switch n := esp.Notificacion; n.Canal {
	case "smtp":
		return enviarCorreo(n.Remitente, d.para, msg)
	case "comentario":
		_, err := leerGH(ctx, "issue", "comment", d.numero, "--repo", d.repo, "--body", msg)
		return err
	case "exec":
		return notificarPorComando(ctx, esp.Dir, n.Comando, t.Est.ID, d.para, msg)
	default:
		return fmt.Errorf("canal %q desconocido", n.Canal)
	}
}

// smtpSendMail es smtp.SendMail (variable para los tests).
var smtpSendMail = smtp.SendMail

// enviarCorreo manda el mensaje por SMTP con el servidor de FABRICA_SMTP_* (nunca de fabrica.yaml).
// ponytail: SendMail usa STARTTLS si el servidor lo ofrece; no cubre TLS implícito (puerto 465).
func enviarCorreo(remitente, para, msg string) error {
	host := os.Getenv("FABRICA_SMTP_HOST")
	if host == "" {
		return errNoConfig{"FABRICA_SMTP_HOST"}
	}
	de, err := mail.ParseAddress(remitente)
	if err != nil {
		return fmt.Errorf("notificacion.remitente inválido: %w", err)
	}
	a, err := mail.ParseAddress(para)
	if err != nil {
		return fmt.Errorf("destinatario inválido %q: %w", para, err)
	}
	var auth smtp.Auth
	if u := os.Getenv("FABRICA_SMTP_USUARIO"); u != "" {
		auth = smtp.PlainAuth("", u, os.Getenv("FABRICA_SMTP_CLAVE"), host)
	}
	addr := net.JoinHostPort(host, cmp.Or(os.Getenv("FABRICA_SMTP_PUERTO"), smtpPuertoPorDefecto))
	cuerpo, err := armarCorreo(de.Address, a.Address, msg)
	if err != nil {
		return err
	}
	return smtpSendMail(addr, auth, de.Address, []string{a.Address}, cuerpo)
}

// armarCorreo: cabeceras con las direcciones ya validadas y el cuerpo en quoted-printable UTF-8.
func armarCorreo(de, para, msg string) ([]byte, error) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n",
		de, para, mime.QEncoding.Encode("utf-8", asuntoNotificacion), time.Now().Format(time.RFC1123Z))
	w := quotedprintable.NewWriter(&buf)
	if _, err := w.Write([]byte(msg)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// notificarPorComando corre el comando de notificacion.comando con el mensaje por stdin.
func notificarPorComando(ctx context.Context, dir, comando, tarea, para, msg string) error {
	ctx, cancel := context.WithTimeout(ctx, execNotificarTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", comando)
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(), "FABRICA_TAREA="+tarea, "FABRICA_PARA="+para)
	cmd.Stdin = strings.NewReader(msg)
	cmd.WaitDelay = 5 * time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, tail(strings.TrimSpace(string(out))))
	}
	return nil
}

// Lo que un mensaje redactado por un modelo nunca lleva: un enlace, un correo o un teléfono
// inyectados en el ticket serían phishing con la firma del equipo.
var (
	reURL      = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)
	reMail     = regexp.MustCompile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)
	reTelefono = regexp.MustCompile(`\+?\d[\d\s().-]{6,}\d`)
)

// limpiarMensaje quita enlaces, correos y teléfonos del borrador y dice si quitó algo. Lo que
// escribe el operador con --mensaje no pasa por acá.
func limpiarMensaje(s string) (string, bool) {
	limpio := s
	for _, re := range []*regexp.Regexp{reURL, reMail, reTelefono} {
		limpio = re.ReplaceAllString(limpio, "")
	}
	limpio = strings.Join(strings.Fields(limpio), " ")
	return limpio, limpio != strings.Join(strings.Fields(s), " ")
}
