// Lo chico que comparten la página y el panel del agente.

export const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
export const corto = (id) => String(id ?? "").slice(-4);
export const usd = (n) => "US$ " + (n || 0).toFixed(2);

export const FUENTE_TXT = { log: "log", ticket: "ticket", tarjeta: "tablero", prompt: "prompt" };
export const tag = (fuente) => `<span class="tag tag-${esc(fuente || "prompt")}">${esc(FUENTE_TXT[fuente] || fuente || "prompt")}</span>`;

export const ESTADO_TXT = {
  EN_CURSO: "▪ en curso", ESPERANDO_APROBACION: "◆ espera la aprobación", TRABADA: "✗ trabada", PROPUESTA: "◆ propuesta",
  PENDIENTE: "· en cola", PR_ABIERTO: "✓ PR abierta", CI_VERDE: "✓ CI verde", CI_ROJO: "✗ CI rojo", INTEGRADA: "✓ mergeada", DESPLIEGUE_PENDIENTE: "· falta desplegar", DESCARTADA: "· descartada",
};
export const estado = (e) => `<span class="estado e-${esc(e)}">${esc(ESTADO_TXT[e] || e)}</span>`;

/** "hace 3 min" contra el reloj del servidor, para que no dependa de la hora de la máquina del navegador. */
// Go manda la fecha cero ("0001-01-01") si 00-entrada.json falta: eso es "sin fecha".
export const conFecha = (ts) => !!ts && new Date(ts).getFullYear() > 2000;

export function hace(ts, ahora) {
  if (!conFecha(ts)) return "";
  const s = Math.max(0, (new Date(ahora) - new Date(ts)) / 1000);
  if (s < 60) return "recién";
  if (s < 3600) return `hace ${Math.floor(s / 60)} min`;
  if (s < 86400) return `hace ${Math.floor(s / 3600)} h`;
  return `hace ${Math.floor(s / 86400)} d`;
}

export const reloj = (ms) => {
  const s = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
};

export const hora = (ts) => new Date(ts).toLocaleTimeString("es-UY", { hour: "2-digit", minute: "2-digit", hour12: false });
export const fecha = (ts) => !conFecha(ts) ? "" : new Date(ts).toLocaleString("es-UY", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hour12: false });

/** Texto de un agente a HTML: escapa y solo entiende `código` y **negrita**. */
export const texto = (s) => esc(s).replace(/`([^`]+)`/g, "<code>$1</code>").replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");

/** Los criterios EARS con sus palabras clave marcadas. */
export const ears = (s) => texto(s).replace(/\b(NO DEBE|DEBE|CUANDO|MIENTRAS|DONDE|SI|ENTONCES)\b/g, '<span class="ears">$1</span>');

/** Links a las PRs de una tarea, abiertos en otra pestaña. */
export const enlacesPR = (urls) => (urls || []).map((u) => `<a class="enlace-pr" href="${esc(u)}" target="_blank" rel="noopener">${esc(u.replace("https://github.com/", "").replace("/pull/", " #"))} ↗</a>`).join(" ");

export const vacio = (titulo, detalle = "") => `<div class="vacio"><strong>${esc(titulo)}</strong>${detalle ? `<p>${detalle}</p>` : ""}</div>`;

