// Geometry for the Knowledge canvas: the outlines drawn around groups, and the
// arrows drawn between them.
//
// A group is a region rather than a tag, so it has to be drawn as one. The
// outline is the convex hull of its members, pushed outwards so nodes sit
// inside rather than on the edge, then rounded — a polygon reads as a diagram,
// a rounded blob reads as an area. Groups overlap by design, which is why these
// are translucent fills and not opaque shapes.

export type Point = [number, number];

/** hull returns the convex hull of pts (monotone chain), counter-clockwise. */
export function hull(pts: Point[]): Point[] {
  if (pts.length < 3) return pts.slice();
  const p = pts.slice().sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const cross = (o: Point, a: Point, b: Point) =>
    (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0]);
  const lower: Point[] = [];
  for (const q of p) {
    while (lower.length >= 2 && cross(lower[lower.length - 2], lower[lower.length - 1], q) <= 0) lower.pop();
    lower.push(q);
  }
  const upper: Point[] = [];
  for (let i = p.length - 1; i >= 0; i--) {
    const q = p[i];
    while (upper.length >= 2 && cross(upper[upper.length - 2], upper[upper.length - 1], q) <= 0) upper.pop();
    upper.push(q);
  }
  lower.pop();
  upper.pop();
  return lower.concat(upper);
}

/** offset pushes each point away from the centroid, so the outline clears the
 *  nodes it encloses instead of passing through them. */
export function offset(pts: Point[], pad: number): Point[] {
  if (!pts.length) return pts;
  const cx = pts.reduce((s, p) => s + p[0], 0) / pts.length;
  const cy = pts.reduce((s, p) => s + p[1], 0) / pts.length;
  return pts.map(([x, y]) => {
    const dx = x - cx;
    const dy = y - cy;
    const l = Math.hypot(dx, dy) || 1;
    return [x + (dx / l) * pad, y + (dy / l) * pad] as Point;
  });
}

function circlePath(cx: number, cy: number, r: number): string {
  return `M${cx - r} ${cy}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0Z`;
}

/** smooth turns a closed polygon into a Catmull-Rom-ish cubic path.
 *
 *  One- and two-member groups have no polygon to round, and would otherwise
 *  vanish or render as a hairline; they become a circle and a capsule so a
 *  small group still looks like a region. */
export function smooth(pts: Point[]): string {
  const n = pts.length;
  if (n === 0) return "";
  if (n === 1) return circlePath(pts[0][0], pts[0][1], 13);
  if (n === 2) {
    const mx = (pts[0][0] + pts[1][0]) / 2;
    const my = (pts[0][1] + pts[1][1]) / 2;
    const r = Math.hypot(pts[0][0] - pts[1][0], pts[0][1] - pts[1][1]) / 2 + 12;
    return circlePath(mx, my, r);
  }
  let d = `M${pts[0][0].toFixed(1)} ${pts[0][1].toFixed(1)}`;
  for (let i = 0; i < n; i++) {
    const p0 = pts[(i - 1 + n) % n];
    const p1 = pts[i];
    const p2 = pts[(i + 1) % n];
    const p3 = pts[(i + 2) % n];
    const c1: Point = [p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6];
    const c2: Point = [p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6];
    d += ` C${c1[0].toFixed(1)} ${c1[1].toFixed(1)},${c2[0].toFixed(1)} ${c2[1].toFixed(1)},${p2[0].toFixed(1)} ${p2[1].toFixed(1)}`;
  }
  return `${d}Z`;
}

/** hullPath is the outline actually drawn for a group. */
export function hullPath(pts: Point[], pad: number): string {
  return smooth(offset(hull(pts), pad));
}

/** centroid of a set of points, used to anchor a group's label and to aim the
 *  relation arrows between two regions. */
export function centroid(pts: Point[]): Point {
  if (!pts.length) return [0, 0];
  return [
    pts.reduce((s, p) => s + p[0], 0) / pts.length,
    pts.reduce((s, p) => s + p[1], 0) / pts.length,
  ];
}

/** arrowHead is a filled triangle at (x,y) pointing along ang (radians). */
export function arrowHead(x: number, y: number, ang: number, size: number): string {
  const a1 = ang + 2.6;
  const a2 = ang - 2.6;
  return (
    `M${x.toFixed(1)} ${y.toFixed(1)}` +
    `L${(x + Math.cos(a1) * size).toFixed(1)} ${(y + Math.sin(a1) * size).toFixed(1)}` +
    `L${(x + Math.cos(a2) * size).toFixed(1)} ${(y + Math.sin(a2) * size).toFixed(1)}Z`
  );
}

/** curveBetween draws a slightly bowed line from a to b and returns both the
 *  path and the head, so an arrow between two regions does not sit exactly on
 *  top of the arrow going the other way. */
export function curveBetween(a: Point, b: Point, gap: number): { path: string; head: string } {
  const dx = b[0] - a[0];
  const dy = b[1] - a[1];
  const len = Math.hypot(dx, dy) || 1;
  const ux = dx / len;
  const uy = dy / len;
  // Pull both ends in so the line starts and stops clear of each region.
  const sx = a[0] + ux * gap;
  const sy = a[1] + uy * gap;
  const ex = b[0] - ux * gap;
  const ey = b[1] - uy * gap;
  // Bow perpendicular to the run, proportional to length but capped so long
  // edges do not swing across unrelated parts of the canvas.
  const bow = Math.min(26, len * 0.16);
  const mx = (sx + ex) / 2 - uy * bow;
  const my = (sy + ey) / 2 + ux * bow;
  const ang = Math.atan2(ey - my, ex - mx);
  return {
    path: `M${sx.toFixed(1)} ${sy.toFixed(1)} Q${mx.toFixed(1)} ${my.toFixed(1)} ${ex.toFixed(1)} ${ey.toFixed(1)}`,
    head: arrowHead(ex, ey, ang, 7),
  };
}
