package fab

import (
	"context"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tareaANotificar: un espacio con el canal dado y una tarea INTEGRADA con su borrador.
func tareaANotificar(t *testing.T, notificacion, origen string) (Config, *Task) {
	t.Helper()
	home := t.TempDir()
	escribirEspacio(t, home, `proyectos:
  demo:
    repos: [{nombre: demo-api, ruta: `+filepath.Join(home, "demo-api")+`}]
    notificacion: {para: solicitante@example.com}
  acme:
    repos: [{nombre: acme-api, ruta: `+filepath.Join(home, "acme-api")+`}]
`+notificacion)
	runs := filepath.Join(home, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	tk, err := addTaskDesde(runs, "agregar filtro", "ticket", Integrada, "demo", origen)
	if err != nil {
		t.Fatal(err)
	}
	if err := tk.WriteJSON(notificacionArchivo, Borrador{Mensaje: "El filtro por fecha ya está en el listado."}); err != nil {
		t.Fatal(err)
	}
	return Config{Home: home, Runs: runs}, tk
}

type correo struct {
	addr, de string
	para     []string
	msg      string
	conAuth  bool
}

func smtpFalso(t *testing.T) *[]correo {
	t.Helper()
	antes := smtpSendMail
	t.Cleanup(func() { smtpSendMail = antes })
	var enviados []correo
	smtpSendMail = func(addr string, a smtp.Auth, de string, para []string, msg []byte) error {
		enviados = append(enviados, correo{addr, de, para, string(msg), a != nil})
		return nil
	}
	t.Setenv("FABRICA_SMTP_HOST", "smtp.example.com")
	t.Setenv("FABRICA_SMTP_PUERTO", "")
	t.Setenv("FABRICA_SMTP_USUARIO", "fabrica")
	t.Setenv("FABRICA_SMTP_CLAVE", "x")
	return &enviados
}

const canalSMTP = "notificacion: {canal: smtp, remitente: fabrica@example.com}\n"

// Si la fuente no declaró el proyecto, el destinatario sale del proyecto de los repos de la spec.
func TestNotificarDeduceElProyectoDeLaSpec(t *testing.T) {
	enviados := smtpFalso(t)
	c, _ := tareaANotificar(t, canalSMTP, "")
	tk, _ := addTaskDesde(c.Runs, "x", "ticket", Integrada, "", "acme/demo-api#3")
	_ = tk.WriteJSON("01-spec.json", Spec{Titulo: "x", Repos: []string{"demo-api"}})
	_ = tk.WriteJSON(notificacionArchivo, Borrador{Mensaje: "Listo."})
	if _, err := c.Notificar(context.Background(), tk.Est.ID, true, "", ""); err != nil {
		t.Fatal(err)
	}
	if len(*enviados) != 1 || (*enviados)[0].para[0] != "solicitante@example.com" {
		t.Fatalf("enviados: %+v", *enviados)
	}
}

// Sin --enviar solo muestra; con --enviar sale una vez, al destinatario del proyecto.
func TestNotificarSMTPUnaSolaVez(t *testing.T) {
	enviados := smtpFalso(t)
	c, tk := tareaANotificar(t, canalSMTP, "")
	ctx := context.Background()
	lines, err := c.Notificar(ctx, tk.Est.ID, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if todo := strings.Join(lines, "\n"); !strings.Contains(todo, "para: solicitante@example.com") || !strings.Contains(todo, "El filtro por fecha") {
		t.Errorf("borrador:\n%s", todo)
	}
	if len(*enviados) != 0 {
		t.Fatal("sin --enviar no sale nada")
	}
	if _, err := c.Notificar(ctx, tk.Est.ID, true, "", ""); err != nil {
		t.Fatal(err)
	}
	if len(*enviados) != 1 {
		t.Fatalf("enviados: %+v", *enviados)
	}
	m := (*enviados)[0]
	if m.addr != "smtp.example.com:587" || m.de != "fabrica@example.com" || m.para[0] != "solicitante@example.com" || !m.conAuth {
		t.Errorf("envío: %+v", m)
	}
	if !strings.Contains(m.msg, "To: solicitante@example.com\r\n") || !strings.Contains(m.msg, "El filtro por fecha ya est=C3=A1 en el listado.") {
		t.Errorf("mensaje:\n%s", m.msg)
	}
	if _, err := c.Notificar(ctx, tk.Est.ID, true, "", ""); err == nil {
		t.Fatal("un segundo --enviar se rechaza")
	}
	if len(*enviados) != 1 {
		t.Fatal("no sale un segundo correo")
	}
}

func TestNotificarSMTPRechaza(t *testing.T) {
	enviados := smtpFalso(t)
	ctx := context.Background()

	c, tk := tareaANotificar(t, canalSMTP, "")
	if _, err := c.Notificar(ctx, tk.Est.ID, true, "a@example.com\r\nBcc: otro@example.com", ""); err == nil {
		t.Error("un destinatario con saltos de línea se rechaza")
	}

	// Sin destinatario: el proyecto acme no declara notificacion.para.
	sinPara, _ := addTaskDesde(c.Runs, "x", "ticket", Integrada, "acme", "")
	_ = sinPara.WriteJSON(notificacionArchivo, Borrador{Mensaje: "Listo."})
	if _, err := c.Notificar(ctx, sinPara.Est.ID, true, "", ""); err == nil || !strings.Contains(err.Error(), "destinatario") {
		t.Errorf("sin destinatario: %v", err)
	}

	// Antes de producción no sale.
	enPR, _ := addTaskDesde(c.Runs, "x", "ticket", PRAbierto, "demo", "")
	_ = enPR.WriteJSON(notificacionArchivo, Borrador{Mensaje: "Listo."})
	if _, err := c.Notificar(ctx, enPR.Est.ID, true, "", ""); err == nil || !strings.Contains(err.Error(), "producción") {
		t.Errorf("en PR: %v", err)
	}

	// Sin servidor configurado no sale.
	t.Setenv("FABRICA_SMTP_HOST", "")
	if _, err := c.Notificar(ctx, tk.Est.ID, true, "", ""); err == nil || !strings.Contains(err.Error(), "FABRICA_SMTP_HOST") {
		t.Errorf("sin host: %v", err)
	}
	if len(*enviados) != 0 {
		t.Fatalf("no tenía que salir nada: %+v", *enviados)
	}
}

func TestNotificarPorComentario(t *testing.T) {
	llamadas := ghFalso(t, "", nil)
	c, tk := tareaANotificar(t, "notificacion: {canal: comentario}\n", "acme/demo-api#12")
	lines, err := c.Notificar(context.Background(), tk.Est.ID, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "comentario en acme/demo-api#12") {
		t.Errorf("destino: %v", lines)
	}
	if len(*llamadas) != 1 || strings.Join((*llamadas)[0], " ") != "issue comment 12 --repo acme/demo-api --body El filtro por fecha ya está en el listado." {
		t.Fatalf("gh: %v", *llamadas)
	}

	// Una tarea que no vino de un issue no tiene dónde comentar.
	_, sinIssue := tareaANotificar(t, "notificacion: {canal: comentario}\n", "")
	c2 := Config{Home: filepath.Dir(sinIssue.runs), Runs: sinIssue.runs}
	if _, err := c2.Notificar(context.Background(), sinIssue.Est.ID, true, "", ""); err == nil || !strings.Contains(err.Error(), "github-issues") {
		t.Errorf("sin issue: %v", err)
	}
}

func TestNotificarPorComando(t *testing.T) {
	t.Setenv("FABRICA_SMTP_CLAVE", "secreto")
	c, tk := tareaANotificar(t, `notificacion: {canal: exec, comando: 'cat > mensaje.txt; echo "$FABRICA_TAREA $FABRICA_PARA ${FABRICA_SMTP_CLAVE:-sin-clave}" > env.txt'}`+"\n", "")
	if _, err := c.Notificar(context.Background(), tk.Est.ID, true, "otra@example.com", ""); err != nil {
		t.Fatal(err)
	}
	msg, _ := os.ReadFile(filepath.Join(c.Home, "mensaje.txt"))
	env, _ := os.ReadFile(filepath.Join(c.Home, "env.txt"))
	if string(msg) != "El filtro por fecha ya está en el listado." {
		t.Errorf("stdin: %q", msg)
	}
	if strings.TrimSpace(string(env)) != tk.Est.ID+" otra@example.com sin-clave" {
		t.Errorf("entorno: %q", env)
	}
}

// Sin canal no se marca nada para redactar: no se gasta una llamada al modelo.
func TestMarcaDeNotificacionSegunCanal(t *testing.T) {
	_, sinCanal := tareaANotificar(t, "", "")
	marcarNotificacionPendiente(sinCanal)
	if _, err := os.Stat(filepath.Join(sinCanal.Dir, notificacionPendiente)); err == nil {
		t.Error("sin canal no se marca")
	}
	_, conCanal := tareaANotificar(t, canalSMTP, "")
	marcarNotificacionPendiente(conCanal)
	if _, err := os.Stat(filepath.Join(conCanal.Dir, notificacionPendiente)); err != nil {
		t.Error("con canal smtp se marca")
	}
	_, comentarioSinIssue := tareaANotificar(t, "notificacion: {canal: comentario}\n", "")
	marcarNotificacionPendiente(comentarioSinIssue)
	if _, err := os.Stat(filepath.Join(comentarioSinIssue.Dir, notificacionPendiente)); err == nil {
		t.Error("con canal comentario, solo las tareas que vinieron de un issue")
	}
}

// El borrador del modelo no lleva enlaces, correos ni teléfonos (pueden venir inyectados desde el ticket).
func TestLimpiarMensaje(t *testing.T) {
	casos := map[string]struct {
		quiero  string
		quitado bool
	}{
		"El filtro ya está disponible.":                              {"El filtro ya está disponible.", false},
		"Ya está. Ingrese a https://evil.example/login para verlo.":  {"Ya está. Ingrese a para verlo.", true},
		"Escriba a soporte@evil.example o llame al +1 555 123 4567.": {"Escriba a o llame al .", true},
		"Ya está en www.evil.example":                                {"Ya está en", true},
		"El pedido 1205 tiene 2 portones de 300 cm × 180 cm.":        {"El pedido 1205 tiene 2 portones de 300 cm × 180 cm.", false},
	}
	for in, c := range casos {
		got, quitado := limpiarMensaje(in)
		if got != c.quiero || quitado != c.quitado {
			t.Errorf("%q → %q (%v), quiero %q (%v)", in, got, quitado, c.quiero, c.quitado)
		}
	}
}
