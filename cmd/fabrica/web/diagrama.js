// El diagrama del cuaderno, dibujado en ASCII sobre una grilla de celdas:
//   logs / tickets / prompt → entrada → spec writer (con el conocimiento arriba) → builder
//   → seguridad | ux | ui | código | resultado → approver (vuelve al builder, máx. 2) → ¿humano? → main | PR → prod
// Cada celda es {ch, c}: el carácter y su clase. Las líneas se arman con bits de dirección para que
// los cruces salgan solos (┬ ┤ ┼); las cajas van encima.

// 137 columnas: cada caja deja al menos una celda de aire a los costados del texto más largo.
export const COLS = 137;
export const FILAS = 21;
const FILA_VUELTA = FILAS - 1;

// pasos: prefijos de evento (runs/<id>/events.jsonl) que prenden el nodo. tecla: atajo 0-9.
export const NODOS = [
  { id: "logs", label: "logs", x: 0, y: 0, w: 13, fuente: ["log"], que: "Incidentes de producción (Better Stack). Entran como propuesta, nunca arrancan solos." },
  { id: "tickets", label: "tickets", x: 0, y: 7, w: 13, fuente: ["ticket", "tarjeta"], que: "Issues de GitHub, tarjetas del tablero con el prefijo configurado y fuentes exec. Un autor no autorizado entra como propuesta." },
  { id: "prompt", label: "prompt", x: 0, y: 14, w: 13, fuente: ["prompt"], que: "Lo que pide el operador, desde esta página o con fabrica add." },
  { id: "entrada", label: "entrada", x: 16, y: 7, w: 12, pasos: ["01"], tecla: "1", que: "Junta todo lo que llega, saca duplicados por huella y tapa los datos personales antes de que lo lea un modelo." },
  { id: "conocimiento", label: "conocimiento", x: 30, y: 0, w: 15, pasos: ["06", "jefe"], tecla: "9", que: "El cerebro: el Jefe contesta con citas del repo de conocimiento; el Bibliotecario anota lo aprendido al cerrar." },
  { id: "spec", label: "spec writer", x: 30, y: 7, w: 15, pasos: ["02"], tecla: "2", que: "Escribe la spec con criterios CA-n en EARS. Si una duda cambia el alcance, frena y lo dice." },
  { id: "builder", label: "builder", x: 47, y: 7, w: 14, pasos: ["03"], tecla: "3", que: "Implementa en una rama aislada con /feature: tests de aceptación primero, después el código, y make check en verde." },
  { id: "seguridad", label: "seguridad", x: 65, y: 0, w: 13, pasos: ["04-seguridad"], tecla: "4", que: "Busca fallas explotables y fugas de datos, con evidencia archivo:línea." },
  { id: "ux", label: "ux", x: 65, y: 4, w: 13, pasos: ["04-ux"], tecla: "5", que: "Recorre las pantallas con Playwright y axe: pasos, teclado y accesibilidad." },
  { id: "ui", label: "ui", x: 65, y: 8, w: 13, pasos: ["04-ui"], tecla: "6", que: "Compara capturas de main contra la rama, en escritorio y celular, contra DISENO.md." },
  { id: "codigo", label: "código", x: 65, y: 12, w: 13, pasos: ["04-codigo"], tecla: "7", que: "Revisa calidad, tamaño y reuso; mutación solo en plata, fechas y auth." },
  // Las cinco cajas de revisión van pegadas (sin fila de aire) para entrar en las 20 filas sobre la vuelta.
  { id: "resultado", label: "resultado", x: 65, y: 16, w: 13, pasos: ["04-resultado"], tecla: "0", que: "Con otro modelo, genera lo que ve el usuario final (pantalla, PDF, correo) con datos de borde y lo mide. Solo si la tarea toca algo visible." },
  { id: "approver", label: "approver", x: 82, y: 7, w: 12, pasos: ["05"], tecla: "8", que: "Junta los hallazgos, descarta falsos positivos con evidencia y decide: pasa, o vuelve al builder (máx. 2 vueltas)." },
  { id: "humano", label: "¿humano?", x: 96, y: 7, w: 12, que: "La persona que aprueba revisa la rama. La aprobación sube la rama, abre la PR y dispara el CI." },
  { id: "main", label: "main", x: 112, y: 2, w: 11, manual: true, que: "El merge a main se hace en la PR, con el CI en verde." },
  { id: "pr", label: "PR", x: 112, y: 12, w: 11, que: "La PR que abre la aprobación, con el CI corriendo." },
  { id: "prod", label: "prod", x: 127, y: 7, w: 10, manual: true, que: "El despliegue a producción. Fuera de la fábrica, salvo el deploy de fabrica.yaml." },
];
for (const n of NODOS) n.h = 4;
export const REVISORES = ["seguridad", "ux", "ui", "codigo", "resultado"];
export const POR_ID = Object.fromEntries(NODOS.map((n) => [n.id, n]));

const N = 1, E = 2, S = 4, W = 8;
const CAJA = { [E | W]: "─", [N | S]: "│", [S | E]: "┌", [S | W]: "┐", [N | E]: "└", [N | W]: "┘", [N | S | E]: "├", [N | S | W]: "┤",
  [E | W | S]: "┬", [E | W | N]: "┴", [N | E | S | W]: "┼", [E]: "─", [W]: "─", [N]: "│", [S]: "│" };
// La línea viva va con trazo grueso: la chispa corre por un riel que se ve.
const PESADA = { [E | W]: "━", [N | S]: "┃", [S | E]: "┏", [S | W]: "┓", [N | E]: "┗", [N | W]: "┛", [N | S | E]: "┣", [N | S | W]: "┫",
  [E | W | S]: "┳", [E | W | N]: "┻", [N | E | S | W]: "╋", [E]: "━", [W]: "━", [N]: "┃", [S]: "┃" };

// Una línea es la lista ordenada de celdas que recorre, para que la chispa vaya de a hacia b.
function linea(a, b, bus) {
  const A = POR_ID[a], B = POR_ID[b], celdas = [];
  const y1 = A.y + 1, y2 = B.y + 1, x1 = A.x + A.w, xf = B.x - 1;
  for (let x = x1; x < (y1 === y2 ? xf : bus); x++) celdas.push([x, y1, E | W]);
  if (y1 !== y2) {
    const baja = y2 > y1, paso = baja ? 1 : -1;
    celdas.push([bus, y1, W | (baja ? S : N)]);
    for (let y = y1 + paso; y !== y2; y += paso) celdas.push([bus, y, N | S]);
    celdas.push([bus, y2, E | (baja ? N : S)]);
    for (let x = bus + 1; x < xf; x++) celdas.push([x, y2, E | W]);
  }
  return { a, b, celdas, flecha: [xf, y2, "▶"] };
}

function lineaConocimiento() {
  const V = POR_ID.conocimiento, S2 = POR_ID.spec, x = V.x + Math.floor(V.w / 2), celdas = [];
  for (let y = V.y + V.h; y < S2.y - 1; y++) celdas.push([x, y, N | S]);
  return { a: "conocimiento", b: "spec", celdas, flecha: [x, S2.y - 1, "▼"] };
}

// La vuelta: del approver baja, corre por debajo de los revisores y sube al builder.
function lineaVuelta() {
  const A = POR_ID.approver, B = POR_ID.builder, celdas = [];
  const xa = A.x + Math.floor(A.w / 2), xb = B.x + Math.floor(B.w / 2);
  for (let y = A.y + A.h; y < FILA_VUELTA; y++) celdas.push([xa, y, N | S]);
  celdas.push([xa, FILA_VUELTA, N | W]);
  for (let x = xa - 1; x > xb; x--) celdas.push([x, FILA_VUELTA, E | W]);
  celdas.push([xb, FILA_VUELTA, N | E]);
  for (let y = FILA_VUELTA - 1; y > B.y + B.h; y--) celdas.push([xb, y, N | S]);
  return { a: "approver", b: "builder", vuelta: true, celdas, flecha: [xb, B.y + B.h, "▲"] };
}

const LINEAS = [
  linea("logs", "entrada", 14), linea("tickets", "entrada", 14), linea("prompt", "entrada", 14),
  linea("entrada", "spec"), lineaConocimiento(), linea("spec", "builder"),
  ...REVISORES.map((r) => linea("builder", r, 62)),
  ...REVISORES.map((r) => linea(r, "approver", 79)),
  linea("approver", "humano"), linea("humano", "main", 109), linea("humano", "pr", 109),
  linea("main", "prod", 124), linea("pr", "prod", 124), lineaVuelta(),
];

/** Cuándo arrancó por última vez el nodo, para mostrar cuánto lleva. */
export const ultimoInicio = (t, n) => {
  let ts = null;
  for (const e of t.eventos || []) if (n.pasos?.some((p) => e.paso.startsWith(p)) && e.estado === "inicio") ts = e.ts;
  return ts;
};

// Después de la aprobación la tarea sigue viva: la PR pasa por el CI y el merge.
const DESPUES_DE_APROBAR = ["PR_ABIERTO", "CI_VERDE", "CI_ROJO", "DESPLIEGUE_PENDIENTE", "INTEGRADA"];

/** Estado de un nodo para una tarea: el último evento de sus pasos manda. */

const ANTES_DEL_BUILDER = ["entrada", "spec", "builder", "conocimiento"];

// Índice del último arranque del builder: ahí empieza la vuelta en curso.
function ultimaVuelta(evs) {
  for (let i = evs.length - 1; i >= 0; i--) if (evs[i].paso.startsWith("03-builder") && evs[i].estado === "inicio") return i;
  return 0;
}

export function estadoNodo(n, t) {
  if (!t) return n.manual ? "manual" : "";
  if (n.manual) return "manual";
  if (n.fuente) return n.fuente.includes(t.fuente) ? "fuente" : "";
  if (t.estado === "DESCARTADA") return n.id === "entrada" ? "ok" : "";
  if (n.id === "humano") return t.estado === "ESPERANDO_APROBACION" ? "espera" : DESPUES_DE_APROBAR.includes(t.estado) ? "ok" : "";
  if (n.id === "pr") return t.estado === "CI_ROJO" ? "falla" : DESPUES_DE_APROBAR.includes(t.estado) ? "ok" : "";
  let ultimo = n.id === "entrada" ? "ok" : ""; // toda tarea pasó por la entrada al crearse
  const evs = t.eventos || [];
  // Lo que viene después del builder (chequeo, revisores, approver) se mira solo en la vuelta en
  // curso: la ✗ de la vuelta anterior quedaba pegada mientras la nueva corría (49a1, 04/11).
  const desde = ANTES_DEL_BUILDER.includes(n.id) ? 0 : ultimaVuelta(evs);
  for (const e of evs.slice(desde)) {
    if (!n.pasos.some((p) => e.paso.startsWith(p))) continue;
    const s = e.estado.toLowerCase();
    ultimo = s === "inicio" ? "corre" : s === "falla" || s === "trabada" ? "falla" : "ok";
  }
  if (n.id === "entrada" && t.estado === "PROPUESTA") return "espera";
  // Un inicio sin cierre en una tarea que ya no corre quedó cortado. Salvo el conocimiento: el
  // Bibliotecario trabaja después de que la tarea cierra.
  if (ultimo === "corre" && t.estado !== "EN_CURSO" && n.id !== "conocimiento") return "falla";
  return ultimo;
}

const ESTADO_TXT = { ok: "✓ listo", falla: "✗ falló", espera: "◆ espera", manual: "a mano", fuente: "› origen", "": "" };
const centrar = (s, w) => {
  const r = [...s].slice(0, w);
  const izq = Math.floor((w - r.length) / 2);
  return " ".repeat(izq) + r.join("") + " ".repeat(w - r.length - izq);
};

/**
 * Arma la grilla para una tarea. cuadro avanza la chispa que recorre la línea viva.
 * conteos: tareas por fuente, para los nodos de entrada. sel: el nodo elegido.
 */
export function armar(t, { cuadro = 0, sel = null, conteos = {} } = {}) {
  const g = Array.from({ length: FILAS }, () => Array.from({ length: COLS }, () => ({ ch: " ", c: "" })));
  const est = Object.fromEntries(NODOS.map((n) => [n.id, estadoNodo(n, t)]));
  const hecho = (s) => s === "ok" || s === "fuente";
  const bits = new Map(), clase = new Map(), prio = { "e-idle": 0, "e-vuelta": 1, "e-hecha": 2, "e-viva": 3, chispa: 3 }; // la chispa es línea viva: nada la pisa

  let vivas = false;
  for (const l of LINEAS) {
    let c = "e-idle";
    if (l.vuelta) c = t && (t.vuelta > 1 || est.approver === "falla") ? "e-vuelta" : "e-idle";
    else if (hecho(est[l.a]) && (est[l.b] === "corre" || est[l.b] === "espera")) c = "e-viva";
    else if (hecho(est[l.a]) && est[l.b] && est[l.b] !== "manual") c = "e-hecha";
    if (c === "e-viva") vivas = true;
    l.celdas.forEach(([x, y, b], i) => {
      const k = y * COLS + x;
      bits.set(k, (bits.get(k) || 0) | b);
      if ((prio[c] ?? 0) >= (prio[clase.get(k)] ?? -1)) {
        // Al approver llegan cinco líneas de largo distinto que comparten el bus: la fase se cuenta desde
        // el final, así las chispas llegan juntas y no se pisan en las celdas compartidas.
        const paso = l.b === "approver" ? l.celdas.length - 1 - i + cuadro : i - cuadro;
        clase.set(k, c === "e-viva" && ((paso % 6) + 6) % 6 === 0 ? "chispa" : c);
      }
    });
    const [fx, fy, fch] = l.flecha, fc = c === "e-viva" ? "chispa" : c;
    if ((prio[fc] ?? 0) >= (prio[g[fy][fx].c] ?? -1)) g[fy][fx] = { ch: fch, c: fc };
  }
  for (const [k, b] of bits) {
    const y = Math.floor(k / COLS), x = k % COLS;
    const viva = clase.get(k) === "e-viva" || clase.get(k) === "chispa";
    g[y][x] = { ch: (viva ? PESADA : CAJA)[b] || "─", c: clase.get(k) };
  }
  const rotulo = " vuelve con fallas · máx 2 ";
  const xr = POR_ID.builder.x + Math.floor(POR_ID.builder.w / 2) + 3; // "└── vuelve…": tres celdas de línea antes del texto
  [...rotulo].forEach((ch, i) => { g[FILA_VUELTA][xr + i] = { ch, c: clase.get(FILA_VUELTA * COLS + xr + i) === "e-vuelta" ? "t-falla" : "t-idle" }; });

  for (const n of NODOS) {
    const s = est[n.id] || "idle";
    let txt = ESTADO_TXT[est[n.id]] ?? "";
    if (s === "corre") txt = "▪ trabaja"; // el movimiento lo pone la órbita de píxeles sobre el marco (app.js)
    if (n.fuente && s !== "fuente") txt = conteos[n.id] ? `${conteos[n.id]} tarea${conteos[n.id] === 1 ? "" : "s"}` : "";
    const label = n.id === "builder" && t?.vuelta > 1 ? `builder v${t.vuelta}` : n.label;
    caja(g, n, s, label, txt, sel === n.id);
  }
  return { g, vivas, est };
}

function caja(g, n, s, label, txt, elegido) {
  const { x, y, w } = n, inner = w - 2, manual = s === "manual";
  // El elegido se marca con borde doble, como una ventana con foco; sin fondo.
  const h = elegido ? "═" : manual ? "┄" : "─", v = elegido ? "║" : manual ? "┆" : "│";
  const bc = `b-${s}`, sel = elegido ? " sel" : "";
  const borde = (r, izq, der) => {
    g[y + r][x] = { ch: izq, c: bc + sel };
    for (let i = 1; i < w - 1; i++) g[y + r][x + i] = { ch: h, c: bc + sel };
    g[y + r][x + w - 1] = { ch: der, c: bc + sel };
  };
  const texto = (r, contenido, c) => {
    g[y + r][x] = { ch: v, c: bc + sel };
    [...centrar(contenido, inner)].forEach((ch, i) => { g[y + r][x + 1 + i] = { ch, c: c + sel }; });
    g[y + r][x + w - 1] = { ch: v, c: bc + sel };
  };
  borde(0, elegido ? "╔" : "┌", elegido ? "╗" : "┐");
  // La tecla del agente en el borde, como el título de un panel de tmux: ┌─3──────┐
  if (n.tecla) g[y][x + 2] = { ch: n.tecla, c: (s === "corre" || s === "espera" ? "t-corre" : "t-idle") + sel };
  texto(1, label, `l-${s}`);
  texto(2, txt, `t-${s}`);
  borde(3, elegido ? "╚" : "└", elegido ? "╝" : "┘");
}

const esc = (ch) => (ch === "<" ? "&lt;" : ch === ">" ? "&gt;" : ch === "&" ? "&amp;" : ch);

/** La grilla como HTML: una corrida de celdas con la misma clase va en un solo span. */
/** El pedazo de la grilla que ocupa un nodo: su caja sola, para dibujarla aparte (el hover la agranda). */
export const recorte = (g, n) => g.slice(n.y, n.y + n.h).map((fila) => fila.slice(n.x, n.x + n.w));

export function html(g) {
  let out = "";
  for (const fila of g) {
    let c = null, buf = "";
    for (const celda of fila) {
      if (celda.c !== c) {
        if (buf) out += c ? `<span class="${c}">${buf}</span>` : buf;
        c = celda.c; buf = "";
      }
      buf += esc(celda.ch);
    }
    if (buf) out += c ? `<span class="${c}">${buf}</span>` : buf;
    out += "\n";
  }
  return out;
}
