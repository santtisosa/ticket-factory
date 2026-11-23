// La página de la fábrica: bandeja (lo que entra), la línea (el diagrama) y el agente elegido.
// Cada 2 s pide /api/estado; solo repinta si algo cambió, así la animación no arranca de cero.
import { NODOS, POR_ID, REVISORES, COLS, FILAS, armar, html, recorte, estadoNodo, ultimoInicio } from "./diagrama.js";
import { panel, estadoPreview } from "./panel.js";
import { ola, dibujarOlas } from "./ola.js";
import { esc, corto, usd, tag, hace, reloj, hora, fecha, vacio, texto, enlacesPR } from "./util.js";

const $ = (id) => document.getElementById(id);
// el.html = "...": escribe en el DOM solo si el HTML cambió. La respuesta trae la hora del servidor, así
// que cada 2 s algo "cambia"; sin esto se re-creaba todo (la órbita volvía a empezar, el hover de la
// card se caía y volvía a crecer, los desplegables del panel se cerraban).
Object.defineProperty(HTMLElement.prototype, "html", {
  set(h) { if (this._html === h) return; this._html = h; this.innerHTML = h; },
});
const quieto = matchMedia("(prefers-reduced-motion: reduce)");

// La bandeja lista solo lo que está por hacerse: trabajando o pendiente. Lo cerrado no aparece.
// Con PR abierta sigue a la vista hasta el merge: el link a la PR y el CI son lo que falta mirar.
const ABIERTAS = ["EN_CURSO", "PENDIENTE", "PROPUESTA", "ESPERANDO_APROBACION", "TRABADA", "PR_ABIERTO", "CI_ROJO", "CI_VERDE"];
// El origen de cada tarea: su color alrededor de la card y su etiqueta.
const ORIGEN = { ticket: "ticket", tarjeta: "ticket", log: "log", prompt: "prompt" };
const ETIQUETA = { ticket: "ticket", log: "log", prompt: "prompt" };
// Un ticket dice de qué proyecto es: "ticket demo".
const etiqueta = (t, origen) => origen === "ticket" && t.cliente ? `ticket ${t.cliente}` : ETIQUETA[origen];
const RANGO = { EN_CURSO: 0, ESPERANDO_APROBACION: 1, TRABADA: 2, CI_ROJO: 2, PROPUESTA: 3, PENDIENTE: 4, PR_ABIERTO: 5, CI_VERDE: 5, DESPLIEGUE_PENDIENTE: 5, INTEGRADA: 6, DESCARTADA: 6 };


let pedido = 0;
// aprobar: el botón de la tarea que espera la aprobación. fase: pidiendo → seco (confirmación) → corriendo; o error.
const ui = { datos: null, firma: "", tarea: null, nodo: null, cuadro: 0, vuelta: null, aprobar: null };

// ── Datos ──────────────────────────────────────────────────────────────────

async function cargar() {
  const mio = ++pedido;
  try {
    const r = await fetch("/api/estado", { cache: "no-store" });
    const txt = await r.text();
    if (mio !== pedido) return; // llegó una respuesta más nueva: esta ya no vale
    if (!r.ok) throw new Error(txt);
    if (txt === ui.firma) return;
    ui.firma = txt;
    ui.datos = JSON.parse(txt);
    // Se relanzó el servidor (quizás con otra página): recargar para no seguir con el código viejo.
    if (ui.arranque && ui.datos.arranque !== ui.arranque) { location.reload(); return; }
    ui.arranque = ui.datos.arranque;
    pintar();
  } catch (e) {
    $("fabrica").className = "barra-estado apagada";
    $("fabrica").html = `<span class="punto"></span>sin conexión con <code>fabrica web</code>`;
  }
}

const tareas = () => (ui.datos?.tareas || []).slice().sort((a, b) => (RANGO[a.estado] ?? 9) - (RANGO[b.estado] ?? 9) || b.id.localeCompare(a.id));
const visibles = () => tareas().filter((t) => ABIERTAS.includes(t.estado));
const actual = () => ui.datos?.tareas.find((t) => t.id === ui.tarea) || null;

// El nodo que se muestra si no elegiste uno: el que trabaja, el que te espera, o el último que pasó.
function nodoPorDefecto(t) {
  if (!t) return "entrada";
  const vivo = NODOS.find((n) => !n.fuente && n.id !== "conocimiento" && ["corre", "espera"].includes(estadoNodo(n, t)));
  if (vivo) return vivo.id;
  const falla = NODOS.find((n) => estadoNodo(n, t) === "falla");
  if (falla) return falla.id;
  const ult = (t.eventos || []).at(-1);
  const n = ult && NODOS.find((x) => x.pasos?.some((p) => ult.paso.startsWith(p)) && x.id !== "conocimiento");
  return n?.id || "entrada";
}

// ── Pintar ─────────────────────────────────────────────────────────────────

function pintar() {
  const ts = tareas();
  if (!ts.some((t) => t.id === ui.tarea)) ui.tarea = (visibles()[0] || ts[0])?.id ?? null;
  // El repintado reemplaza los botones: el foco vuelve al mismo, así el teclado no se pierde cada 2 s.
  const d = document.activeElement?.dataset || {};
  const foco = d.id ? `.item-boton[data-id="${CSS.escape(d.id)}"]` : d.nodo ? `.nodo-boton[data-nodo="${d.nodo}"]` : null;
  pintarBarra();
  pintarBandeja();
  pintarMesa();
  if (foco) document.querySelector(foco)?.focus();
}

function pintarBarra() {
  const d = ui.datos;
  const enCurso = d.tareas.find((t) => t.estado === "EN_CURSO");
  const el = $("fabrica");
  if (!d.corriendo && !enCurso) {
    el.className = "barra-estado apagada";
    el.html = `<span class="punto"></span>apagada <span class="quieto">· ejecutar <code>./fabrica run</code></span>`;
  } else if (enCurso) {
    const n = POR_ID[nodoPorDefecto(enCurso)];
    el.className = "barra-estado trabajando";
    el.html = `<span class="punto"></span>trabajando <span class="quieto">· ${esc(corto(enCurso.id))} en ${esc(n.label)}</span>`;
  } else {
    el.className = "barra-estado prendida";
    const enCola = d.tareas.filter((t) => t.estado === "PENDIENTE").length;
    el.html = `<span class="punto"></span>prendida <span class="quieto">· ${enCola ? `${enCola} en cola` : "esperando tareas"}</span>`;
  }

  const cuantas = (e) => d.tareas.filter((t) => t.estado === e).length;
  $("pie-tareas").textContent = `${d.tareas.length} tareas · ${cuantas("EN_CURSO")} en curso · ${cuantas("PENDIENTE")} en cola`;

  const g = $("gasto");
  if (!d.tope_dia) { // con la suscripción no hay tope: solo el equivalente gastado
    g.className = "barra-gasto";
    g.html = `hoy ${usd(d.gastado_hoy)} · sin tope`;
    g.title = "Equivalente en US$ de lo que corrió hoy. Se usa la suscripción: no hay tope (FABRICA_TOPE_DIA_USD lo vuelve a poner).";
    return;
  }
  const CELDAS = 14, parte = Math.min(1, d.gastado_hoy / (d.tope_dia || 1)), llenas = Math.round(parte * CELDAS);
  g.className = "barra-gasto" + (parte >= 1 ? " tope" : parte >= 0.8 ? " alto" : "");
  g.html = `hoy <span class="barras"><span class="lleno">${"■".repeat(llenas)}</span>${"·".repeat(CELDAS - llenas)} </span>${usd(d.gastado_hoy)} de ${usd(d.tope_dia)}`;
  g.title = "Gasto del día contra el tope (FABRICA_TOPE_DIA_USD)";
}

function pintarBandeja() {
  const vs = visibles();
  $("tab-n").textContent = vs.length || "";
  parchar($("grupos"), vs.length ? vs.map(item).join("")
    : `<p class="vacio-chico">Nada por hacer. Lo que llegue de las fuentes de fabrica.yaml, de los incidentes o de un prompt aparece aquí.</p>`);
}

// Con las mismas cards en el mismo orden, cambia solo el contenido de cada una: la card bajo el puntero
// sigue siendo el mismo nodo, así el hover no se cae y vuelve a crecer en cada repintado.
function parchar(cont, h) {
  if (cont._html === h) return;
  cont._html = h;
  const nuevo = document.createElement("div");
  nuevo.innerHTML = h;
  const sel = ".item", viejos = cont.querySelectorAll(sel), nuevos = nuevo.querySelectorAll(sel);
  const llave = (l) => [...l].map((x) => x.dataset.item || "·").join();
  if (llave(viejos) !== llave(nuevos)) { cont.replaceChildren(...nuevo.childNodes); return; }
  viejos.forEach((v, i) => {
    if (v.innerHTML !== nuevos[i].innerHTML) v.innerHTML = nuevos[i].innerHTML;
    if (v.dataset.item) v.setAttribute("aria-current", nuevos[i].getAttribute("aria-current"));
  });
}


// La pista de cada card: los tramos de la línea, con el actual pintado. Los revisores van juntos.
const TRAMOS = [["entrada", ["entrada"]], ["spec", ["spec"]], ["builder", ["builder"]], ["revisión", REVISORES], ["approver", ["approver"]], ["aprobación", ["humano"]]];
const SUBPASOS = { "03-chequeo": "chequeo" };

/** El subpaso del builder en curso: el último evento de la línea es de un subpaso (su inicio o el
 *  cierre de uno de sus repos). Si después arrancó el builder o un revisor, no hay subpaso vivo. */
function subpasoVivo(t) {
  const evs = t.eventos || [];
  for (let i = evs.length - 1; i >= 0; i--) {
    const e = evs[i];
    if (SUBPASOS[e.paso]) {
      const ini = evs.slice(0, i + 1).reverse().find((x) => x.paso === e.paso && x.estado === "inicio");
      return { label: SUBPASOS[e.paso], ts: ini ? ini.ts : e.ts };
    }
    if (e.paso.startsWith("03-builder") || e.paso.startsWith("04") || e.paso.startsWith("05")) return null;
  }
  return null;
}

const GLIFO = { ok: "✓", corre: "▪", espera: "◆", falla: "✗" };

function estadoTramo(t, ids) {
  const ss = ids.map((id) => estadoNodo(POR_ID[id], t));
  return ["corre", "espera", "falla"].find((k) => ss.includes(k)) || (ss.includes("ok") ? "ok" : "");
}

function pista(t) {
  const tramos = TRAMOS.map(([txt, ids]) => {
    const s = estadoTramo(t, ids);
    return `<span class="t-${s || "idle"}">${s === "corre" || s === "espera" || s === "falla" ? GLIFO[s] + " " : ""}${txt}</span>`;
  });
  return `<span class="pista" aria-hidden="true">${tramos.join('<span class="sep">·</span>')}</span>`;
}

/** En una frase, dónde está la tarea: lo que la card y la cabeza dicen primero. */
function ahora(t) {
  switch (t.estado) {
    case "EN_CURSO": return `▪ trabajando en ${POR_ID[nodoPorDefecto(t)].label}`;
    case "TRABADA": {
      const f = NODOS.find((n) => estadoNodo(n, t) === "falla");
      return `✗ trabada${f ? ` en ${f.label}` : ""}`;
    }
    case "ESPERANDO_APROBACION": return "◆ espera la aprobación";
    case "PROPUESTA": return "◆ propuesta · a decidir si entra";
    case "PENDIENTE": return "· en cola";
    case "PR_ABIERTO": return "✓ PR abierta";
    case "CI_VERDE": return "✓ PR con CI verde · falta tu merge";
    case "CI_ROJO": return "✗ PR con CI rojo";
    case "INTEGRADA": return "✓ mergeada";
    case "DESPLIEGUE_PENDIENTE": return "· mergeada, falta desplegar";
    case "DESCARTADA": return "· descartada";
  }
  return t.estado;
}

function item(t) {
  const origen = ORIGEN[t.fuente] || "prompt", trabajando = t.estado === "EN_CURSO";
  return `<div class="item origen-${origen}" data-item="${esc(t.id)}" aria-current="${t.id === ui.tarea}">${trabajando ? ola(0.5) : ""}
    <button class="item-boton" data-id="${esc(t.id)}">
      <span class="item-arriba"><span class="etiqueta">${esc(etiqueta(t, origen))}</span>
        <span class="estado-item ${trabajando ? "trabajando" : "pendiente"}">${trabajando ? "▪ trabajando" : "· pendiente"}</span>
        <span class="cuando">${esc(hace(t.creada, ui.datos.ahora))}</span></span>
      <span class="item-titulo">${esc(t.titulo)}</span>
      <span class="item-abajo"><span class="ahora-item e-${esc(t.estado)}">${esc(ahora(t))}</span>${t.costo_usd ? `<span>${usd(t.costo_usd)}</span>` : ""}</span>
      ${pista(t)}
    </button>${t.id === ui.tarea ? `<div class="item-acciones solo-angosto"><button class="boton boton-chico" data-pestana="detalle">Ver detalle →</button></div>` : ""}</div>`;
}

function pintarMesa() {
  const t = actual();
  if (!t) {
    $("cabeza").html = `<div class="linea-arriba"><span>sin tareas</span></div><h2>Esperando tickets, logs o prompts</h2>`;
    $("accion").html = "";
    $("agente").html = "";
    $("registro").html = "";
    pintarDiagrama();
    pintarBotones(null);
    return;
  }
  const paso = POR_ID[nodoPorDefecto(t)];
  $("cabeza").html = `<div class="linea-arriba">${tag(t.fuente)}<span>${esc(corto(t.id))}</span>
      <span class="ahora estado e-${esc(t.estado)}">${esc(ahora(t))}</span></div>
    <h2 title="${esc(t.titulo)}">${esc(primeraLinea(t))}</h2>
    ${detalle(t) ? `<p class="linea-texto">${esc(detalle(t))}</p>` : ""}
    <div class="linea-meta"><span>entró <b>${esc(hace(t.creada, ui.datos.ahora) || "sin fecha")}</b>${fecha(t.creada) ? ` · ${esc(fecha(t.creada))}` : ""}</span>
      <span>paso <b>${esc(paso.label)}</b></span>${t.vuelta ? `<span>vuelta <b>${t.vuelta}</b></span>` : ""}<span>gastó <b>${usd(t.costo_usd)}</b></span></div>`;
  $("accion").html = accion(t);
  pintarDiagrama();
  pintarBotones(t);
  pintarAgente();
  $("registro").html = (t.eventos || []).slice().reverse().map((e) => `<li><span class="hora">${esc(hora(e.ts))}</span>
    <span class="paso p-${esc(e.estado.toLowerCase())}">${esc(e.paso)}</span><span class="msj">${texto(e.mensaje)}</span>
    <span class="costo">${e.costo_usd ? usd(e.costo_usd) : ""}</span></li>`).join("") || `<li class="vacio-chico">Sin eventos todavía.</li>`;
}

const comando = (c) => `<div class="comando"><span class="prompt">$</span><code>${esc(c)}</code><button class="boton boton-chico" data-copiar="${esc(c)}">Copiar</button></div>`;

// El título es una línea; la descripción entera va debajo. Sin spec todavía, el título es el texto
// entero: la cabeza muestra su primera línea y, si esa entra sin cortarse, abajo va solo el resto.
const TITULO_ENTRA = 70;
const primeraLinea = (t) => (t.titulo || "").trim().split("\n")[0];
function detalle(t) {
  const x = (t.texto || "").trim(), p = primeraLinea(t);
  if (!x || x === p) return "";
  return p.length <= TITULO_ENTRA && x.startsWith(p) ? x.slice(p.length).trim() : x;
}

function accion(t) {
  const apagada = !ui.datos.corriendo ? " La fábrica está apagada: no arranca hasta que corras <code>./fabrica run</code>." : "";
  switch (t.estado) {
    case "PROPUESTA":
      return `<div class="accion a-espera"><div class="accion-texto"><strong>Entró por ${t.fuente === "log" ? "un log" : "un ticket"} y espera que decidas.</strong>
        <p>Si se acepta, pasa a la cola y la línea la toma. Si no, queda descartada y no vuelve a entrar. Se decide en la terminal.</p></div>
        <div>${comando(`./fabrica aceptar ${t.id}`)}${comando(`./fabrica descartar ${t.id}`)}</div></div>`;
    case "ESPERANDO_APROBACION": return accionAprobar(t);
    case "TRABADA":
      return `<div class="accion a-falla"><div class="accion-texto"><strong>Se trabó.</strong><p>${esc(t.motivo)}</p>
        <p>Si se detuvo la spec, se responde con <code>fabrica continuar</code>; si fue otra causa y ya está resuelta, <code>fabrica reintentar</code>.</p></div>
        <div>${comando(`./fabrica continuar ${t.id} "<respuestas>"`)}${comando(`./fabrica reintentar ${t.id}`)}</div></div>`;
    case "EN_CURSO": {
      const n = POR_ID[nodoPorDefecto(t)];
      // Dentro del builder corre el chequeo: se nombra el paso que corre de verdad (05/11: decía
      // "builder" mientras corría el chequeo).
      const sub = subpasoVivo(t);
      const label = sub ? sub.label : n.label, desde = sub ? sub.ts : ultimoInicio(t, n);
      return `<div class="accion"><div class="accion-texto"><strong>Trabajando en ${esc(label)}${desde ? ` · <span id="cronometro" data-desde="${esc(desde)}">${reloj(new Date(ui.datos.ahora) - new Date(desde))}</span>` : ""}</strong>
</div></div>`;
    }
    case "PENDIENTE":
      return `<div class="accion"><div class="accion-texto"><strong>En cola.</strong><p>Arranca en cuanto la línea esté libre: una tarea a la vez.${apagada}</p></div></div>`;
    case "DESCARTADA":
      return `<div class="accion"><div class="accion-texto"><strong>Descartada.</strong><p>${esc(t.motivo || "No va a correr.")}</p></div></div>`;
    case "CI_ROJO":
      return `<div class="accion a-falla"><div class="accion-texto"><strong>La PR tiene el CI rojo.</strong><p class="prs">${enlacesPR(t.prs)}</p><p>${esc(t.motivo || "")} Se arregla en la rama de la PR, como cualquier CI rojo.</p></div></div>`;
    case "DESPLIEGUE_PENDIENTE":
      return `<div class="accion a-falla"><div class="accion-texto"><strong>Integrada, falta desplegar.</strong><p class="prs">${enlacesPR(t.prs)}</p><p>${esc(t.motivo || "La línea la despliega en la próxima vuelta.")} A mano: <code>./fabrica desplegar ${esc(t.id)}</code>.</p></div></div>`;
    case "CI_VERDE":
    case "INTEGRADA":
    case "PR_ABIERTO":
      return `<div class="accion a-ok"><div class="accion-texto"><strong>${{ CI_VERDE: "PR con el CI verde.", INTEGRADA: "PR mergeada." }[t.estado] || "PR abierta, corre el CI."}</strong>
        <p class="prs">${enlacesPR(t.prs)}</p>${t.estado === "INTEGRADA" ? "" : "<p>El merge sigue el proceso habitual del repositorio.</p>"}</div></div>`;
    default:
      return "";
  }
}

// ── Diagrama ───────────────────────────────────────────────────────────────

function conteos() {
  const c = {};
  for (const t of ui.datos?.tareas || []) for (const n of NODOS) if (n.fuente?.includes(t.fuente)) c[n.id] = (c[n.id] || 0) + 1;
  return c;
}

function pintarDiagrama() {
  const t = actual();
  const nodo = ui.nodo || nodoPorDefecto(t);
  const { g } = armar(t, { cuadro: ui.cuadro, sel: nodo, conteos: conteos() });
  $("diagrama").innerHTML = html(g);
  ui.grilla = g;
}


// Una caja pegada a un borde del diagrama crece hacia adentro: si crece desde el centro, el scroll
// del diagrama le corta el costado que sale.
const origen = (n) => `${n.x <= 1 ? "left" : n.x + n.w >= COLS - 1 ? "right" : "center"} ${n.y <= 1 ? "top" : n.y + n.h >= FILAS - 1 ? "bottom" : "center"}`;

function pintarBotones(t) {
  const nodo = ui.nodo || nodoPorDefecto(t);
  $("diagrama-botones").html = NODOS.map((n, i) => {
    const s = estadoNodo(n, t) || "sin pasar";
    return `<button class="nodo-boton" data-nodo="${n.id}" tabindex="${n.id === nodo ? 0 : -1}" aria-pressed="${n.id === nodo}" style="--x:${n.x};--y:${n.y};--w:${n.w};--h:${n.h};transform-origin:${origen(n)}"
      aria-label="${esc(n.label)}: ${esc(s)}${n.tecla ? `, atajo ${n.tecla}` : ""}" title="${esc(n.label)} — ${esc(n.que)}">${ui.grilla ? `<pre class="caja-copia" aria-hidden="true">${html(recorte(ui.grilla, n)).trimEnd()}</pre>` : ""}${s === "corre" ? ola((i % 4) / 4) : ""}</button>`;
  }).join("");
}

let pedidoPanel = 0;
async function pintarAgente() {
  const t = actual();
  if (!t) return;
  const nodo = ui.nodo || nodoPorDefecto(t), mio = ++pedidoPanel;
  const contenido = await panel(t, nodo, ui);
  if (mio === pedidoPanel) $("agente").html = contenido; // si cambiaste de nodo mientras leía, gana el último
}

// La letra de la grilla se ajusta al ancho de la card: el diagrama la ocupa entera.
let proporcion = 0.6;
function medir() {
  const s = document.createElement("span");
  s.style.cssText = "position:absolute;visibility:hidden;font:100px var(--mono);white-space:pre";
  s.textContent = "0".repeat(50);
  document.body.append(s);
  proporcion = s.getBoundingClientRect().width / 5000 || 0.6;
  s.remove();
  ajustar();
}
function ajustar() {
  const ancho = $("diagrama-caja").clientWidth;
  const px = Math.max(9.5, Math.min(22, ancho / (COLS * proporcion) - 0.02));
  $("diagrama-caja").style.setProperty("--celda", `${px.toFixed(2)}px`);
  // Si a 9,5 px no entra, el diagrama se desplaza y un fundido a la derecha avisa que sigue.
  requestAnimationFrame(() => $("diagrama-caja").classList.toggle("desborda", $("diagrama").scrollWidth > $("diagrama-caja").clientWidth + 1));
}

// ── Animación: solo si algo trabaja o te espera ─────────────────────────────

const ONDA = " .·:-=+*#";
function animar() {
  const t = actual();
  const vivo = ui.datos?.tareas.some((x) => x.estado === "EN_CURSO");
  const mueve = !quieto.matches && t && NODOS.some((n) => ["corre", "espera"].includes(estadoNodo(n, t)));
  if (mueve) { ui.cuadro++; pintarDiagrama(); }
  const p = $("pulso");
  if (vivo && !quieto.matches) {
    const n = Math.ceil(anchoPulso / 6);
    let s = "";
    for (let i = 0; i < n; i++) {
      const l = Math.abs(Math.sin(i * 0.08 - ui.cuadro * 0.35) * Math.sin(i * 0.013 + ui.cuadro * 0.05));
      s += ONDA[Math.floor(l * (ONDA.length - 1))];
    }
    p.textContent = s;
    if (!mueve) ui.cuadro++;
  } else if (p.textContent) p.textContent = "";
  const cr = document.getElementById("cronometro");
  if (cr) { const v = reloj(Date.now() - new Date(cr.dataset.desde)); if (cr.textContent !== v) cr.textContent = v; }
}

// ── Acciones ───────────────────────────────────────────────────────────────

let avisoHasta = 0;
function avisar(msg, error = false) {
  const a = $("aviso");
  a.textContent = msg;
  a.className = "aviso visible" + (error ? " error" : "");
  clearTimeout(avisoHasta);
  avisoHasta = setTimeout(() => { a.className = "aviso" + (error ? " error" : ""); }, 3200);
}

function elegirTarea(id, enfocar = false) {
  ui.tarea = id;
  ui.nodo = null;
  ui.vuelta = null;
  history.replaceState(null, "", `#${id}`);
  pintarBandeja();
  pintarMesa();
  if (enfocar) document.querySelector(`.item-boton[data-id="${CSS.escape(id)}"]`)?.focus();
}

// La aprobación desde la página: primero el seco (lo que haría), después tu confirmación. Lo mismo que
// ./fabrica aprobar; el servidor solo lo acepta de esta página.
async function aprobar(paso) {
  const t = actual();
  if (!t) return;
  const repintar = () => { $("accion").html = accion(actual() || t); };
  if (paso === "no") { ui.aprobar = null; repintar(); return; }
  const seco = paso === "ver";
  ui.aprobar = { id: t.id, fase: seco ? "pidiendo" : "corriendo", lineas: ui.aprobar?.lineas || [] };
  repintar();
  try {
    const r = await fetch(`/api/tareas/${encodeURIComponent(t.id)}/aprobar${seco ? "?seco=1" : ""}`, { method: "POST", headers: { "X-Fabrica": "aprobar" } });
    const j = await r.json().catch(() => ({ error: `el servidor respondió ${r.status}` }));
    if (!r.ok || j.error) throw new Error(j.error || `el servidor respondió ${r.status}`);
    if (seco) ui.aprobar = { id: t.id, fase: "seco", lineas: j.lineas || [] };
    else { ui.aprobar = null; avisar("Aprobada: el PR quedó abierto y corre el CI."); ui.firma = ""; cargar(); }
  } catch (e) {
    ui.aprobar = { id: t.id, fase: "error", error: e.message, lineas: [] };
  }
  repintar();
}

// El botón de la vista previa: el servidor contesta enseguida y la levanta aparte; el avance llega
// por /api/estado cada 2 s.
async function levantarPreview(boton) {
  boton.disabled = true;
  try {
    const r = await fetch(`/api/tareas/${encodeURIComponent(boton.dataset.preview)}/preview`, { method: "POST", headers: { "X-Fabrica": "preview" } });
    const j = await r.json().catch(() => ({ error: `el servidor respondió ${r.status}` }));
    if (!r.ok || j.error) throw new Error(j.error || `el servidor respondió ${r.status}`);
    avisar("Levantando la vista previa: tarda un minuto o más.");
  } catch (e) {
    avisar(`No se pudo levantar: ${e.message}`, true);
    boton.disabled = false;
  }
  ui.firma = "";
  cargar();
}

function accionAprobar(t) {
  const rama = (t.motivo || "").replace(/^lista para la aprobación:\s*/, "");
  const d = ui.aprobar?.id === t.id ? ui.aprobar : null;
  const terminal = `<details class="por-terminal"><summary>o por la terminal</summary>${comando(`./fabrica aprobar ${t.id} --seco`)}${comando(`./fabrica aprobar ${t.id}`)}</details>`;
  const previa = estadoPreview(t, ui);
  let texto = `<p>La rama <code>${esc(rama)}</code> pasó a los cuatro revisores. Lo que hizo está abajo, en <b>¿humano?</b>. Aceptar sube lo aprobado, abre la PR y corre el CI. Antes te muestro qué va a hacer.</p>`;
  let botones = `<button class="boton boton-primario" data-aprobar="ver">Aceptar y correr el CI</button>`;
  if (d?.fase === "pidiendo") botones = `<button class="boton" disabled>Preparando…</button>`;
  if (d?.fase === "corriendo") botones = `<button class="boton" disabled>Responda el diálogo de confirmación; después se sube la rama y se abre la PR…</button>`;
  if (d?.fase === "seco" || d?.fase === "corriendo") {
    texto = `<p>Esto es lo que se va a ejecutar. Al confirmar, sale de esta máquina:</p><pre class="pre-largo aprobar-seco">${esc(d.lineas.join("\n"))}</pre>`;
    if (d.fase === "seco") botones = `<button class="boton boton-primario" data-aprobar="si">Confirmar: subir y abrir la PR (después, el diálogo de confirmación)</button><button class="boton" data-aprobar="no">Cancelar</button>`;
  }
  if (d?.fase === "error") {
    texto += `<p class="aprobar-error">No salió: ${esc(d.error)}</p>`;
    botones = `<button class="boton boton-primario" data-aprobar="ver">Probar de nuevo</button><button class="boton" data-aprobar="no">Cerrar</button>`;
  }
  return `${previa}<div class="accion a-espera"><div class="accion-texto"><strong>Lista para la aprobación.</strong>${texto}
    <div class="accion-botones">${botones}</div>${terminal}</div></div>`;
}

// Las pestañas solo se ven a media pantalla; en ancho, bandeja y detalle están a la vista a la vez.
function pestana(cual) {
  $("grilla").dataset.pestana = cual;
  for (const b of document.querySelectorAll(".pestanas [data-pestana]")) b.setAttribute("aria-selected", String(b.dataset.pestana === cual));
}

function elegirNodo(id) {
  ui.nodo = ui.nodo === id ? null : id;
  if (ui.nodo) pestana("detalle");
  if (ui.tarea) history.replaceState(null, "", `#${ui.tarea}${ui.nodo ? "/" + ui.nodo : ""}`);
  pintarDiagrama();
  pintarBotones(actual());
  pintarAgente();
}

// Los links a otra pestaña (la PR, la vista previa) los abre el servidor en el navegador por
// defecto: esta ventana es Chrome en modo app y no tiene la sesión de GitHub.
document.addEventListener("click", (ev) => {
  const a = ev.target.closest('a[target="_blank"]');
  if (!a || ev.defaultPrevented) return;
  ev.preventDefault();
  fetch("/api/abrir", { method: "POST", headers: { "X-Fabrica": "abrir", "Content-Type": "application/json" }, body: JSON.stringify({ url: a.href }) })
    .then((r) => { if (!r.ok) throw new Error(r.status); })
    .catch(() => window.open(a.href, "_blank", "noopener"));
}, true);

document.addEventListener("click", (ev) => {
  const el = ev.target.closest("[data-copiar],[data-aprobar],[data-preview],[data-pestana],[data-nodo],[data-vuelta],.item-boton");
  if (!el) return;
  if (el.dataset.copiar) {
    navigator.clipboard.writeText(el.dataset.copiar).then(() => avisar("Copiado. Pegar en la terminal."), () => avisar("No se pudo copiar.", true));
    return;
  }
  if (el.dataset.aprobar) { aprobar(el.dataset.aprobar); return; }
  if (el.dataset.preview) { levantarPreview(el); return; }
  if (el.dataset.pestana) { pestana(el.dataset.pestana); return; }
  if (el.dataset.nodo) return elegirNodo(el.dataset.nodo);
  if (el.dataset.vuelta) { ui.vuelta = Number(el.dataset.vuelta); pintarAgente(); return; }
  if (el.dataset.id) elegirTarea(el.dataset.id);
});

document.addEventListener("keydown", (ev) => {
  if (ev.target.closest("textarea, input") || ev.metaKey || ev.ctrlKey || ev.altKey) return;
  if (ev.key === "Escape") { if (ui.nodo) elegirNodo(ui.nodo); return; }
  if (ev.key === "j" || ev.key === "k") {
    const vs = visibles(), i = vs.findIndex((t) => t.id === ui.tarea);
    const sig = vs[Math.min(vs.length - 1, Math.max(0, i + (ev.key === "j" ? 1 : -1)))];
    if (sig) elegirTarea(sig.id, true);
  } else {
    const n = NODOS.find((x) => x.tecla === ev.key);
    if (n) elegirNodo(n.id);
  }
});

// ── Arranque ───────────────────────────────────────────────────────────────

const [hashTarea, hashNodo] = location.hash.slice(1).split("/");
if (hashTarea) ui.tarea = hashTarea;
if (hashNodo && POR_ID[hashNodo]) ui.nodo = hashNodo;
pestana("bandeja");
new ResizeObserver(ajustar).observe($("diagrama-caja"));
let anchoPulso = 0;
new ResizeObserver(([e]) => { anchoPulso = e.contentRect.width; }).observe($("pulso"));
document.fonts.ready.then(medir);
cargar();
setInterval(cargar, 2000);
let tick = -1;
let ultimaOla = 0;
const bucle = (t) => {
  const c = Math.floor(t / 110);
  if (c !== tick) { tick = c; animar(); }
  if (t - ultimaOla >= 33) { ultimaOla = t; dibujarOlas(t, proporcion, quieto.matches); }
  requestAnimationFrame(bucle);
};
requestAnimationFrame(bucle);
