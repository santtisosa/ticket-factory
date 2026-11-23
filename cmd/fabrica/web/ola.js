// La ola del que trabaja: un anillo de píxeles alrededor de la caja (o de la card) por el que corre
// una ola que se levanta hacia afuera y da toda la vuelta. Cada píxel tiene su tono de azul, y la
// cresta se aclara a medida que sube. Se dibuja en un <canvas class="ola"> que vive adentro del
// elemento: el mismo efecto sirve para las cajas del diagrama y para las cards de la bandeja.

const PIXEL = 3;          // lado de cada píxel, en px de CSS
const MARGEN = 14;        // lugar alrededor del elemento para que la ola suba sin cortarse
const VUELTA_MS = 5200;   // una vuelta completa de la cresta: lenta, se ve subir
const ALTURA = 3;         // píxeles que se apilan en la cresta
const ANCHO_CRESTA = 7;   // en píxeles del anillo

// Tonos de la base (al azar estable por píxel) y de cada piso de la ola, del más bajo al más alto.
const TONOS_BASE = ["#3B82F6", "#4C9AFF", "#2563EB", "#60A5FA"];
const TONOS_PISO = ["#4C9AFF", "#7AB8FF", "#A5CFFF", "#D6E8FF"];

const azar = (k) => {
  const n = Math.sin(k * 127.1 + 311.7) * 43758.5453;
  return n - Math.floor(n);
};

// Los puntos del anillo, en sentido horario, cada PIXEL px, con su normal hacia afuera.
function anillo(x0, y0, x1, y1) {
  const pts = [];
  for (let x = x0; x < x1; x += PIXEL) pts.push([x, y0, 0, -1]);
  for (let y = y0; y < y1; y += PIXEL) pts.push([x1, y, 1, 0]);
  for (let x = x1; x > x0; x -= PIXEL) pts.push([x, y1, 0, 1]);
  for (let y = y1; y > y0; y -= PIXEL) pts.push([x0, y, -1, 0]);
  return pts;
}

// Cuánto se levanta el píxel k de n, con la cresta en c (0..1): la cresta, un eco más chico atrás
// y un rizo suave por todo el anillo, para que nunca quede quieto del todo.
function altura(k, n, c, t) {
  const s = k / n;
  const dist = (a) => Math.min(Math.abs(s - a), 1 - Math.abs(s - a)) * n;
  const cresta = Math.exp(-((dist(c) / ANCHO_CRESTA) ** 2));
  const eco = 0.5 * Math.exp(-((dist((c - 0.14 + 1) % 1) / (ANCHO_CRESTA * 0.8)) ** 2));
  const rizo = 0.12 * (1 + Math.sin(2 * Math.PI * (s * 9 - t / 1800)));
  return Math.min(1, cresta + eco + rizo);
}

function dibujar(cv, t, chPorEm, quieto) {
  const el = cv.parentElement;
  const W = el.offsetWidth, H = el.offsetHeight; // sin el scale del hover: el canvas escala con el elemento
  const dpr = window.devicePixelRatio || 1;
  const cw = W + 2 * MARGEN, ch = H + 2 * MARGEN;
  if (cv.width !== Math.round(cw * dpr) || cv.height !== Math.round(ch * dpr)) {
    cv.width = Math.round(cw * dpr);
    cv.height = Math.round(ch * dpr);
  }
  const ctx = cv.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, cw, ch);

  // En el diagrama el borde son caracteres: la línea pasa por el centro de la primera y la última
  // celda. El anillo va 4 px por fuera de esa línea. En una card, pegado al borde.
  let x0 = MARGEN - 1, y0 = MARGEN - 1, x1 = MARGEN + W + 1, y1 = MARGEN + H + 1;
  if (el.classList.contains("nodo-boton")) {
    const fs = parseFloat(getComputedStyle(el).fontSize);
    const mx = 0.5 * chPorEm * fs - 4, my = 0.6 * fs - 4;
    x0 = MARGEN + mx; y0 = MARGEN + my; x1 = MARGEN + W - mx; y1 = MARGEN + H - my;
  }
  const snap = (v) => Math.round(v / PIXEL) * PIXEL; // los píxeles caen en una grilla, como en una pantalla
  const pts = anillo(snap(x0), snap(y0), snap(x1), snap(y1));
  const fase = Number(cv.dataset.fase || 0);
  const c = quieto ? 0.15 : (((t / VUELTA_MS) + fase) % 1);

  pts.forEach(([x, y, nx, ny], k) => {
    const h = quieto ? 0.25 : altura(k, pts.length, c, t);
    // la base: siempre, tenue donde la ola no pasa
    ctx.globalAlpha = 0.22 + 0.7 * h;
    ctx.fillStyle = TONOS_BASE[Math.floor(azar(k) * TONOS_BASE.length)];
    ctx.fillRect(x - PIXEL / 2, y - PIXEL / 2, PIXEL - 0.5, PIXEL - 0.5);
    // los pisos: la ola se levanta hacia afuera y se aclara arriba
    const pisos = Math.round(h * ALTURA);
    for (let j = 1; j <= pisos; j++) {
      ctx.globalAlpha = 0.95 - j * 0.12;
      ctx.fillStyle = TONOS_PISO[Math.min(TONOS_PISO.length - 1, j - 1 + (azar(k + j * 31) > 0.7 ? 1 : 0))];
      ctx.fillRect(x + nx * PIXEL * j - PIXEL / 2, y + ny * PIXEL * j - PIXEL / 2, PIXEL - 0.5, PIXEL - 0.5);
    }
  });
  ctx.globalAlpha = 1;
}

/** Dibuja todas las olas de la página. chPorEm: el ancho de un carácter de la grilla, en em. */
export function dibujarOlas(t, chPorEm, quieto) {
  for (const cv of document.querySelectorAll("canvas.ola")) dibujar(cv, t, chPorEm, quieto);
}

/** El canvas para meter adentro de un elemento que trabaja. La fase corre a cada uno un poco. */
export const ola = (fase = 0) => `<canvas class="ola" aria-hidden="true" data-fase="${fase}"></canvas>`;
