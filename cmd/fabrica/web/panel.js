// El panel del agente elegido: qué hace, qué entregó (spec, build, hallazgos, capturas, decisión)
// y su parte del registro. Lee los archivos de runs/<id>/ por /api/tareas/<id>/archivos/<nombre>.
import { POR_ID, REVISORES, estadoNodo } from "./diagrama.js";
import { esc, usd, hora, fecha, texto, ears, vacio, FUENTE_TXT, tag, enlacesPR } from "./util.js";

const cache = new Map();

async function leer(t, nombre, comoTexto = false) {
  if (!t.archivos?.includes(nombre)) return null;
  // La clave cambia con cada evento nuevo: si el agente reescribió su salida, se vuelve a leer.
  const clave = `${t.id}:${nombre}:${t.eventos?.length || 0}`;
  if (!cache.has(clave)) {
    cache.set(clave, fetch(`/api/tareas/${t.id}/archivos/${nombre}`).then((r) => {
      if (!r.ok) throw new Error(`no se pudo leer ${nombre}`);
      return comoTexto ? r.text() : r.json();
    }).catch((e) => { cache.delete(clave); throw e; }));
  }
  return cache.get(clave);
}

/** Las vueltas disponibles de un archivo con -vN, de la última a la primera. */
const vueltas = (t, prefijo) => (t.archivos || [])
  .map((a) => a.match(new RegExp(`^${prefijo}-v(\\d+)\\.json$`))?.[1]).filter(Boolean).map(Number).sort((a, b) => b - a);

const ESTADO_NODO = { ok: "✓ listo", falla: "✗ falló", corre: "▪ trabaja", espera: "◆ espera", manual: "a mano", fuente: "› origen de esta tarea", "": "· sin pasar" };

const eventosDe = (t, n) => (t.eventos || []).filter((e) => n.pasos?.some((p) => e.paso.startsWith(p)));

function registroCorto(evs) {
  if (!evs.length) return "";
  return `<div class="bloque"><h4>Lo que hizo</h4><ol class="registro">${evs.slice().reverse().map((e) => `<li>
    <span class="hora">${hora(e.ts)}</span><span class="paso p-${esc(e.estado.toLowerCase())}">${esc(e.paso)}</span>
    <span class="msj">${texto(e.mensaje)}</span><span class="costo">${e.costo_usd ? usd(e.costo_usd) : ""}</span></li>`).join("")}</ol></div>`;
}

function hallazgo(h) {
  const donde = [h.repo, h.archivo && `${h.archivo}${h.linea ? ":" + h.linea : ""}`].filter(Boolean).join(" · ");
  return `<li class="hallazgo sev-${esc(h.severidad)}">
    <div class="hallazgo-arriba"><span class="sev">${esc(h.severidad || "—")}</span>${donde ? `<span>${esc(donde)}</span>` : ""}
      ${h.regla ? `<span>${esc(h.regla)}</span>` : ""}${h.ca ? `<span>${esc(h.ca)}</span>` : ""}${h.confianza ? `<span>confianza ${esc(h.confianza)}/10</span>` : ""}
      ${h.revisor ? `<span>${esc(h.revisor)}</span>` : ""}</div>
    <p>${texto(h.detalle)}</p>${h.evidencia ? `<pre>${esc(h.evidencia)}</pre>` : ""}</li>`;
}

const listaHallazgos = (hs) => hs?.length ? `<ul class="hallazgos">${hs.map(hallazgo).join("")}</ul>` : "";

function entrada(t, n) {
  if (n.fuente && !n.fuente.includes(t.fuente)) {
    return vacio(`Esta tarea no entró por ${n.label}.`, `Entró como ${esc(FUENTE_TXT[t.fuente] || t.fuente)}. Para ver lo que llegó por esta fuente, filtre la bandeja.`);
  }
  const sensible = t.sensible?.length ? `<div class="bloque"><h4>Toca algo sensible</h4><div class="chips">${t.sensible.map((s) => `<span class="chip chip-alerta">${esc(s)}</span>`).join("")}</div></div>` : "";
  return `<div class="bloque"><h4>Lo que entró · ${tag(t.fuente)} · ${esc(fecha(t.creada))}</h4>
    <p class="texto-largo">${esc(t.texto || t.titulo)}</p></div>${sensible}`;
}

async function spec(t) {
  const s = await leer(t, "01-spec.json");
  if (!s) return vacio("Todavía no hay spec.", "El Spec writer la escribe apenas la tarea sale de la entrada.");
  const md = await leer(t, "01-spec.md", true).catch(() => null);
  const chips = (rot, xs) => xs?.length ? `<span class="chip">${esc(rot)}: ${xs.map(esc).join(", ")}</span>` : "";
  const CLAVE = { defaultRecomendado: "Asumido", impacto: "Impacto", pregunta: "Pregunta" };
  const dudas = (s.faltaAclarar || []).map((d) => `<li>${Object.entries(d).map(([k, v]) => `<strong>${esc(CLAVE[k] || k)}:</strong> ${texto(typeof v === "string" ? v : JSON.stringify(v))}`).join(" · ")}</li>`).join("");
  return `${s.frenar ? `<div class="accion a-falla"><div class="accion-texto"><strong>El Spec writer frenó</strong><p>${texto(s.motivoFrenar)}</p></div></div>` : ""}
    <div class="chips">${s.tipo ? `<span class="chip">${esc(s.tipo)}</span>` : ""}${chips("repos", s.repos)}${chips("rutas", s.rutas)}</div>
    <div class="bloque"><h4>Criterios</h4><ul class="criterios">${(s.criterios || []).map((c) => `<li><span class="ca">${esc(c.id)}</span><span>${ears(c.texto)}</span></li>`).join("")}</ul></div>
    ${dudas ? `<div class="bloque"><h4>Asumido o por aclarar</h4><ul>${dudas}</ul></div>` : ""}
    ${md ? `<details class="bloque"><summary class="rotulo">Spec completa · 01-spec.md</summary><pre class="pre-largo">${esc(md)}</pre></details>` : ""}`;
}

async function builder(t, ui) {
  const vs = vueltas(t, "02-build");
  if (!vs.length) return vacio("Todavía no construyó.", "El Builder arranca con la spec lista.");
  const v = vs.includes(ui.vuelta) ? ui.vuelta : vs[0];
  const b = await leer(t, `02-build-v${v}.json`);
  const tabs = vs.length > 1 ? `<div class="vueltas" role="group" aria-label="Vuelta">${vs.slice().reverse().map((n) => `<button class="filtro" data-vuelta="${n}" aria-pressed="${n === v}">vuelta ${n}</button>`).join("")}</div>` : "";
  const tests = (b.tests_aceptacion || []).map((x) => `<li><span class="ca">${esc(x.ca)}</span><span><code>${esc(x.archivo)}</code> ${esc(x.test || "")}</span></li>`).join("");
  return `${tabs}${!b.listo && b.bloqueo ? `<div class="accion a-falla"><div class="accion-texto"><strong>No terminó</strong><p>${texto(b.bloqueo)}</p></div></div>` : ""}
    <div class="bloque"><h4>Qué hizo</h4><p class="texto-largo">${texto(b.resumen)}</p></div>
    ${tests ? `<div class="bloque"><h4>Tests de aceptación</h4><ul class="criterios">${tests}</ul></div>` : ""}`;
}

async function revisor(t, n) {
  const r = await leer(t, `03-${n.id}.json`);
  if (!r) return vacio("Todavía no revisó.", "Los revisores corren en paralelo cuando el Builder termina.");
  const ok = r.veredicto === "ok" && !r.error;
  let extra = "";
  if (n.id === "ui" || n.id === "ux") {
    const v = (t.archivos || []).map((a) => a.match(/^ui-v(\d+)\//)?.[1]).filter(Boolean).map(Number).sort((a, b) => b - a)[0];
    if (v && n.id === "ui") {
      const pngs = t.archivos.filter((a) => a.startsWith(`ui-v${v}/`) && a.endsWith(".png"));
      extra = `<div class="bloque"><h4>Capturas de la rama · vuelta ${v}</h4><div class="capturas">${pngs.map((p) => {
        const url = `/api/tareas/${t.id}/archivos/${p}`, nombre = p.split("/")[1].replace(".png", "");
        return `<figure class="captura"><a href="${url}" target="_blank" rel="noopener"><img src="${url}" alt="Captura ${esc(nombre)}" loading="lazy"></a><figcaption>${esc(nombre)}</figcaption></figure>`;
      }).join("")}</div></div>`;
    }
    if (v && n.id === "ux") {
      const res = await leer(t, `ui-v${v}/resumen.json`).catch(() => null);
      if (res?.length) extra = `<div class="bloque"><h4>Recorrido · vuelta ${v}</h4><ul class="criterios">${res.map((x) => `<li><span class="ca">${esc(x.status)}</span><span><code>${esc(x.ruta)}</code> en ${esc(x.vista)}${x.axe != null ? ` · ${esc(x.axe)} fallas de axe` : ""}</span></li>`).join("")}</ul></div>`;
    }
  }
  return `<p><span class="veredicto ${ok ? "ok" : "falla"}">${ok ? "✓ sin objeciones" : `✗ ${esc(r.error || r.veredicto)}`}</span>
    ${r.hallazgos?.length ? ` · ${r.hallazgos.length} hallazgo${r.hallazgos.length === 1 ? "" : "s"}` : ""}</p>
    ${listaHallazgos(r.hallazgos)}${extra}`;
}

async function approver(t) {
  const vs = vueltas(t, "04-approver");
  if (!vs.length) return vacio("Todavía no decidió.", "Decide cuando los revisores entregan.");
  const d = await leer(t, `04-approver-v${vs[0]}.json`);
  const desc = d.descartados?.length ? `<details class="bloque"><summary class="rotulo">${d.descartados.length} descartados (no bloquean)</summary>${listaHallazgos(d.descartados)}</details>` : "";
  return `<p><span class="veredicto ${d.pasa ? "ok" : "falla"}">${d.pasa ? "✓ pasa" : "✗ vuelve al builder"}</span> · vuelta ${esc(d.vuelta)}</p>
    ${d.motivos?.length ? `<div class="bloque"><h4>Motivos</h4><ul>${d.motivos.map((m) => `<li>${texto(m)}</li>`).join("")}</ul></div>` : ""}
    ${d.bloquean?.length ? `<div class="bloque"><h4>Bloquean</h4>${listaHallazgos(d.bloquean)}</div>` : ""}${desc}`;
}

async function conocimiento(t) {
  const b = await leer(t, "05-bibliotecario.json");
  if (!b) return vacio("El Bibliotecario todavía no pasó.", "Anota lo aprendido cuando la tarea cierra.");
  if (b.nada) return `<div class="bloque"><h4>Nada nuevo para el cerebro</h4><p class="texto-largo">${texto(b.motivo)}</p></div>`;
  return `<div class="bloque"><h4>Lo que anotó en el conocimiento</h4><ul>${(b.escrituras || []).map((e) => `<li>${typeof e === "string" ? texto(e) : Object.entries(e).map(([k, v]) => `<strong>${esc(k)}:</strong> ${texto(String(v))}`).join(" · ")}</li>`).join("")}</ul></div>`;
}

// probar: la app de la rama levantada en la máquina local y qué mirar en ella, sacado de la spec (criterios
// y lo asumido) y del Approver (lo que quedó a confirmar o sin revisar). Sin otro agente.
async function probar(t, ui) {
  const s = await leer(t, "01-spec.json").catch(() => null);
  const vs = vueltas(t, "04-approver");
  const d = vs.length ? await leer(t, `04-approver-v${vs[0]}.json`).catch(() => null) : null;
  const rutas = (pv.rutas || []).map((r) => `<li><a href="${esc(pv.url + r)}" target="_blank" rel="noopener"><code>${esc(r)}</code> ↗</a></li>`).join("");
  const criterios = (s?.criterios || []).map((c) => `<li><label class="check"><input type="checkbox"><span class="ca">${esc(c.id)}</span><span>${ears(c.texto)}</span></label></li>`).join("");
  const asumido = (s?.faltaAclarar || []).map((x) => x.defaultRecomendado).filter(Boolean).map((x) => `<li>${texto(x)}</li>`).join("");
  const ojo = [...(d?.a_confirmar || []).map((h) => `${h.severidad} ${h.archivo || ""}: ${h.detalle}`), ...(d?.sin_revisar || [])].map((x) => `<li>${texto(x)}</li>`).join("");
  return `${estadoPreview(t, ui)}
    ${rutas ? `<div class="bloque"><h4>Dónde mirar</h4><ul>${rutas}</ul></div>` : ""}
    ${criterios ? `<div class="bloque"><h4>Qué deberías ver</h4><ul class="criterios checklist">${criterios}</ul></div>` : ""}
    ${asumido ? `<div class="bloque"><h4>Lo que la fábrica asumió (a confirmar)</h4><ul>${asumido}</ul></div>` : ""}
    ${ojo ? `<div class="bloque"><h4>Ojo: nadie lo revisó o quedó a confirmar</h4><ul>${ojo}</ul></div>` : ""}`;
}

// La vista previa de la tarea: prendida (hasta qué hora), levantándose, apagada o rota. Las tres
// últimas con el botón para levantarla de nuevo.
export function estadoPreview(t, ui) {
  const pv = ui.datos?.preview?.id === t.id ? ui.datos.preview : null;
  const boton = (rotulo) => `<div class="accion-botones"><button class="boton boton-primario" data-preview="${esc(t.id)}">${esc(rotulo)}</button></div>`;
  const caja = (clase, titulo, cuerpo) => `<div class="accion ${clase} probar"><div class="accion-texto"><strong>${titulo}</strong>${cuerpo}</div></div>`;
  if (pv?.levantando) return caja("", "Levantando la vista previa…", "<p>La app de la rama arranca con la receta <code>preview</code> del proyecto: puede tardar un minuto o más.</p>");
  if (pv?.error) return caja("a-falla", "La vista previa no levantó.", `<p>${texto(pv.error)}</p>${boton("Probar de nuevo")}`);
  if (pv?.apagada) return caja("", "La vista previa se apagó.", `<p>Se apaga sola a los 20 minutos para no cargar la máquina.</p>${boton("Levantarla de nuevo")}`);
  if (!pv?.url) return caja("", "Sin vista previa para esta tarea.", `<p>Hay una sola a la vez.</p>${boton("Levantar la vista previa")}`);
  return caja("a-ok", "Vista previa", `<p><a href="${esc(pv.url)}" target="_blank" rel="noopener">${esc(pv.url)} ↗</a></p>
      <p>Es la rama de esta tarea, levantada con la receta <code>preview</code> del proyecto en esta máquina. Se apaga sola a las <b>${esc(hora(pv.hasta))}</b>, al aprobar o al cancelar la tarea.</p>`);
}

// Lo necesario para decidir la aprobación, junto: qué pidió la spec, qué hizo el Builder, qué dijo el
// Approver y lo sensible. Es lo mismo que va al cuerpo de la PR.
async function humano(t, ui) {
  if (t.estado === "ESPERANDO_APROBACION") {
    const sensible = t.sensible?.length ? `<div class="bloque"><h4>Toca algo sensible</h4><div class="chips">${t.sensible.map((s) => `<span class="chip chip-alerta">${esc(s)}</span>`).join("")}</div><p>Conviene probarlo a mano en el entorno antes de darlo por terminado.</p></div>` : "";
    const seccion = (titulo, h) => `<section class="contexto"><h4 class="rotulo">${esc(titulo)}</h4>${h}</section>`;
    return `${await probar(t, ui)}<p>Arriba está el botón para aceptar. Esto es lo que hizo la fábrica:</p>${sensible}
      ${seccion("Builder", await builder(t, ui))}
      ${seccion("Approver", await approver(t))}
      ${seccion("Spec", await spec(t))}`;
  }
  if (t.prs?.length) return `<p>Ya diste la aprobación. La PR: ${enlacesPR(t.prs)}</p>`;
  return vacio("Todavía no llegó a esta etapa.", "La tarea llega a la aprobación cuando el Approver la deja pasar.");
}

/** El panel entero de un nodo. ui guarda lo que el usuario eligió adentro (la vuelta del build). */
export async function panel(t, nodoId, ui) {
  const n = POR_ID[nodoId];
  const s = estadoNodo(n, t);
  const evs = eventosDe(t, n);
  const gasto = evs.reduce((a, e) => a + (e.costo_usd || 0), 0);
  let cuerpo;
  try {
    // Mientras trabaja por primera vez, el panel lo dice: "todavía no" es solo para lo que no arrancó.
    if (s === "corre" && !evs.some((e) => e.estado === "ok")) cuerpo = vacio("Trabajando ahora.", "Cuando termine, aquí aparece lo que entregó.");
    else if (n.fuente || n.id === "entrada") cuerpo = entrada(t, n);
    else if (n.id === "spec") cuerpo = await spec(t);
    else if (n.id === "builder") cuerpo = await builder(t, ui);
    else if (REVISORES.includes(n.id)) cuerpo = await revisor(t, n);
    else if (n.id === "approver") cuerpo = await approver(t);
    else if (n.id === "conocimiento") cuerpo = await conocimiento(t);
    else if (n.id === "humano") cuerpo = await humano(t, ui);
    else cuerpo = "";
  } catch (e) {
    cuerpo = vacio("No se pudo leer lo que entregó.", esc(e.message));
  }
  return `<div class="agente-cabeza"><h3>${esc(n.label)}</h3>
      <span class="estado t-${s || "idle"}">${esc(ESTADO_NODO[s] ?? s)}${gasto ? ` · ${usd(gasto)}` : ""}</span></div>
    <p class="agente-que">${esc(n.que)}</p>
    <div class="agente-cuerpo">${cuerpo}${registroCorto(evs)}</div>`;
}
