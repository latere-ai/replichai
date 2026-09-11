// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The hero scene (033): the mark as a field of particles, lit, in fog, over a
// receding floor.
//
// It is the same argument as `brand.tsx`'s drawn mark and the same geometry —
// an inner orbit continuous all the way round, an outer orbit with six
// permanent voids baked into its buffer — so the identity and the claim stay
// one thing at any scale. The voids belong to the geometry rather than to a
// shader trick, which is why the outer loop visibly never closes however long
// it is watched.
//
// three is imported dynamically and nowhere else in the app, so Vite splits it
// into a chunk the first paint never pays for. Everything here is best-effort:
// no WebGL, no chunk, or a throw on any line leaves the caller with an empty
// slab and a page that reads exactly as designed.

// The scene has two states, because the slab does (040). What changes between
// them is not a colour list — it is the *arithmetic*.
//
// In the dark the mark is light added to a void: every layer blends additively,
// each particle's brightness lives in its colour channels, and where particles
// crowd, the sum climbs towards white. On paper that operation has no meaning —
// adding light to a pale ground saturates on the first layer and the mark
// vanishes. So the light state inks instead: normal blending, the hue held
// constant, and the brightness moved into an alpha channel. Crowding then
// converges on full ink exactly as crowding converged on white, so density
// still reads as density and the figure means what it meant.
//
// Everything the two disagree about is in one Palette. The geometry below reads
// it and never branches on the theme itself.
export type HeroTheme = 'light' | 'dark'

type Palette = {
  /** Clear colour and fog: the slab's own ground. */
  ground: number
  /** Light adds, paper inks. */
  additive: boolean
  /**
   * Turns a layer's brightness into alpha in the inked state. The brightness
   * values overshoot 1 on purpose — they were written to saturate an additive
   * sum — so this scales them into a range where a faint particle stays faint.
   */
  gain: number
  /** Multiplies every layer's material opacity. */
  ink: number
  floor: number
  grid: readonly [number, number]
  gridOpacity: number
  hues: {
    inner: number
    runner: number
    outer: number
    wireIn: number
    wireOut: number
    rays: number
  }
  /** The core, outermost first: [scale, opacity, hue]. */
  core: readonly (readonly [number, number, number])[]
  lights: {
    sky: number
    below: number
    hemi: number
    key: number
    fill: number
    fillPower: number
    warm: number
    warmPower: number
    rim: number
  }
  dust: readonly [number, number]
}

const DARK: Palette = {
  ground: 0x0a0b0d,
  additive: true,
  gain: 1,
  ink: 1,
  floor: 0x0d1014,
  grid: [0x1c2836, 0x141b24],
  gridOpacity: 0.5,
  hues: { inner: 0x3d86e0, runner: 0xbcd9fb, outer: 0xd2554f, wireIn: 0x9cc4f2, wireOut: 0xe8a8a3, rays: 0x7fb6f0 },
  core: [
    [7.5, 0.1, 0x5f93d8],
    [4.6, 0.13, 0x86b4ec],
    [2.7, 0.16, 0xa8cbf5],
    [1.5, 0.2, 0xd6e8fc],
    [0.75, 0.26, 0xffffff],
  ],
  lights: { sky: 0x9fc4ee, below: 0x08090b, hemi: 0.5, key: 2.1, fill: 0x2369bd, fillPower: 40, warm: 0xa9373b, warmPower: 26, rim: 1.1 },
  dust: [0x9fb4d0, 0.55],
}

// Paper. The hue ladder inverts with the ground: in the dark the hottest layer
// is the palest (the runner, near white), and on paper the hottest layer is the
// deepest, because that is what stands out against the page.
const LIGHT: Palette = {
  ground: 0xeceae5,
  additive: false,
  gain: 0.62,
  ink: 0.85,
  floor: 0xe8e6e0,
  grid: [0xdcd9d1, 0xe4e1d9],
  gridOpacity: 0.3,
  hues: { inner: 0x2369bd, runner: 0x0f3b6d, outer: 0xa9373b, wireIn: 0x14477f, wireOut: 0x86282c, rays: 0x3d78bd },
  // Tighter than the dark core on purpose: an additive star reads as light and
  // a wide inked one reads as a smudge, so the paper version keeps its reach
  // short and spends the contrast at the centre.
  core: [
    [7.5, 0.05, 0x9ab6d8],
    [4.6, 0.07, 0x6f95c4],
    [2.7, 0.11, 0x4a74ab],
    [1.5, 0.16, 0x2b5590],
    [0.75, 0.32, 0x123a6d],
  ],
  lights: { sky: 0xffffff, below: 0xd9d6ce, hemi: 0.9, key: 1.2, fill: 0x2369bd, fillPower: 14, warm: 0xa9373b, warmPower: 9, rim: 0.4 },
  dust: [0x8f9bab, 0.34],
}

type HeroScene = { dispose: () => void }

// The names browsers give a rasteriser that is not a GPU. SwiftShader is
// Chrome's, llvmpipe and softpipe are Mesa's, WARP is Direct3D's; "software
// adapter" catches the generic wording. Matched case-insensitively against the
// unmasked renderer string.
const SOFTWARE_GL = /swiftshader|llvmpipe|softpipe|warp|software|basic render/i

// isSoftwareRenderer asks what is actually behind the context.
//
// The debug extension is the only way to see through the masked string a browser
// reports by default; where it is unavailable the masked value is still worth
// testing, because SwiftShader names itself there too. Unknown is treated as
// hardware — a real GPU misread as software would cost the scene for nothing,
// and that is the worse of the two mistakes.
function isSoftwareRenderer(renderer: import('three').WebGLRenderer): boolean {
  try {
    const gl = renderer.getContext()
    const ext = gl.getExtension('WEBGL_debug_renderer_info')
    const name = String(
      (ext && gl.getParameter(ext.UNMASKED_RENDERER_WEBGL)) || gl.getParameter(gl.RENDERER) || '',
    )
    return SOFTWARE_GL.test(name)
  } catch {
    return false
  }
}

// mount builds the scene inside `el` and starts it. `reduce` renders exactly one
// frame and never schedules another — the resting state of this figure is the
// figure, not a first frame of it. `stage` is the element pointer movement is
// measured against, which is the whole slab rather than the canvas, so the
// parallax follows the cursor over the copy as well.
export async function mountHeroScene(
  el: HTMLElement,
  stage: HTMLElement | null,
  reduce: boolean,
  theme: HeroTheme = 'dark',
): Promise<HeroScene | null> {
  const pal = theme === 'light' ? LIGHT : DARK
  let THREE: typeof import('three')
  try {
    THREE = await import('three')
  } catch {
    return null
  }
  let renderer: import('three').WebGLRenderer
  try {
    renderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: 'high-performance' })
  } catch {
    // No WebGL context. The slab stands on its own.
    return null
  }

  // A context is not the same as a GPU. Where hardware acceleration is absent
  // the browser hands back a *software* rasteriser — SwiftShader, llvmpipe — and
  // it will honour every draw call by rasterising 25,000 additively-blended
  // particles on the CPU, every frame, forever. That does not degrade the page;
  // it takes it over. The main thread stops yielding, the rest of the document
  // loads at a crawl, and the reader gets a janky first screen where a still one
  // would have been better.
  //
  // So a software renderer is treated as no renderer at all: the same outcome as
  // a missing context, which the slab is already designed to stand up to.
  if (isSoftwareRenderer(renderer)) {
    renderer.dispose()
    return null
  }

  const w = el.clientWidth || 1200
  const h = el.clientHeight || 700
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
  renderer.setSize(w, h)
  // The filmic curve is what makes an additive bloom roll off instead of
  // clipping. Inked on paper there is no bloom to roll off, and the curve only
  // greys the ground, so the light state renders its values as written.
  if (pal.additive) {
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    renderer.toneMappingExposure = 1.15
  }
  renderer.setClearColor(pal.ground, 1)
  renderer.domElement.style.display = 'block'
  el.appendChild(renderer.domElement)

  const scene = new THREE.Scene()
  scene.fog = new THREE.FogExp2(pal.ground, 0.036)
  const camera = new THREE.PerspectiveCamera(38, w / h, 0.1, 300)
  camera.position.set(0.4, 1.05, 13.2)
  camera.lookAt(0, 0.1, 0)

  // Floor: a plane plus a grid, both eaten by fog, so the object sits in a place
  // rather than on a colour.
  const floor = new THREE.Mesh(
    new THREE.PlaneGeometry(300, 300),
    new THREE.MeshStandardMaterial({ color: pal.floor, roughness: 0.62, metalness: 0.28 }),
  )
  floor.rotation.x = -Math.PI / 2
  floor.position.y = -4.2
  scene.add(floor)
  const grid = new THREE.GridHelper(160, 80, pal.grid[0], pal.grid[1])
  grid.position.y = -4.18
  const gridMat = grid.material as import('three').Material
  gridMat.transparent = true
  gridMat.opacity = pal.gridOpacity
  scene.add(grid)

  // The rig only sways. A solid mark turning on its axis reads as a spinning
  // icon, and the meaning here is in how the material moves, not in the
  // silhouette.
  const rig = new THREE.Group()
  rig.rotation.x = -1.02
  rig.rotation.z = 0.1
  rig.position.set(2.0, 1.1, 0)
  scene.add(rig)

  // One radial-falloff texture does both jobs: it turns every point from a hard
  // square into a soft grain, and it is the core's glow. A three-stop ramp would
  // hold near-solid alpha across the middle, and additive blending then clips
  // that plateau to pure white — so the plateau's boundary shows up as a hard
  // circle. Falling off from the very first pixel means nothing ever plateaus.
  const glowTex = (() => {
    const c = document.createElement('canvas')
    c.width = c.height = 128
    const ctx = c.getContext('2d')!
    const g = ctx.createRadialGradient(64, 64, 0, 64, 64, 64)
    for (let i = 0; i <= 24; i++) {
      const t = i / 24
      g.addColorStop(t, 'rgba(255,255,255,' + (Math.exp(-5.2 * t * t) * (1 - t * t)).toFixed(4) + ')')
    }
    ctx.fillStyle = g
    ctx.fillRect(0, 0, 128, 128)
    return new THREE.CanvasTexture(c)
  })()

  const dot = (size: number, opacity: number) =>
    new THREE.PointsMaterial({
      size,
      map: glowTex,
      vertexColors: true,
      transparent: true,
      opacity: opacity * pal.ink,
      depthWrite: false,
      blending: pal.additive ? THREE.AdditiveBlending : THREE.NormalBlending,
      sizeAttenuation: true,
      fog: false,
    })

  // Every layer writes its per-particle brightness through here. Additively it
  // goes into the colour, so the sum climbs; inked it goes into alpha, so the
  // hue holds and the coverage climbs. The colour attribute is RGBA either way
  // — three reads a four-wide colour attribute as vertex alpha — which keeps
  // one buffer layout and one code path for both.
  const paint = (col: Float32Array, i: number, c: import('three').Color, b: number) => {
    const k = i * 4
    if (pal.additive) {
      col[k] = c.r * b
      col[k + 1] = c.g * b
      col[k + 2] = c.b * b
      col[k + 3] = 1
    } else {
      col[k] = c.r
      col[k + 1] = c.g
      col[k + 2] = c.b
      col[k + 3] = Math.min(1, b * pal.gain)
    }
  }

  // A uniform sample across [rIn,rOut] ends a band at a hard radius, which in
  // near-edge-on projection reads as a straight shear across the disc. Three
  // averaged randoms give a bell instead, so density falls to nothing at both
  // rims and the band has no edge to cut on.
  const bell = () => (Math.random() + Math.random() + Math.random()) / 3

  // `arcs` slices the orbit into that many spans and fills `duty` of each, so the
  // gaps are geometry. Each arc is feathered along its own length as well, so a
  // segment dissolves at its ends instead of stopping square.
  const orbit = (count: number, rIn: number, rOut: number, thick: number, arcs: number, duty: number, hex: number, bright: number) => {
    const pos = new Float32Array(count * 3)
    const col = new Float32Array(count * 4)
    const c = new THREE.Color(hex)
    const span = (Math.PI * 2) / arcs
    for (let i = 0; i < count; i++) {
      const k = Math.floor(Math.random() * arcs)
      const u = bell()
      const rr = rIn + u * (rOut - rIn)
      const radial = 1 - Math.abs(u - 0.5) * 2
      let a: number
      let along = 1
      if (arcs === 1) {
        a = Math.random() * Math.PI * 2
      } else {
        // Overrun the nominal span slightly so the feather has somewhere to go.
        const t = -0.08 + Math.random() * 1.16
        a = k * span + t * span * duty
        along = Math.sin(Math.PI * Math.min(1, Math.max(0, t)))
      }
      pos[i * 3] = Math.cos(a) * rr
      pos[i * 3 + 1] = Math.sin(a) * rr
      pos[i * 3 + 2] = (bell() - 0.5) * thick * 2
      paint(col, i, c, bright * (0.12 + 0.88 * radial * along) * (0.35 + Math.random() * 0.65))
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('color', new THREE.BufferAttribute(col, 4))
    return g
  }

  // The runner: the one arc that travels the closed loop, which is what "re-runs"
  // looks like. It reaches zero brightness at both ends rather than stopping at
  // full, and still leans forward, so the run keeps its direction without the
  // leading edge terminating on a straight cut.
  const arcBand = (count: number, rIn: number, rOut: number, thick: number, aSpan: number, hex: number, bright: number) => {
    const pos = new Float32Array(count * 3)
    const col = new Float32Array(count * 4)
    const c = new THREE.Color(hex)
    for (let i = 0; i < count; i++) {
      const u = Math.random()
      const a = u * aSpan
      const v = bell()
      const rr = rIn + v * (rOut - rIn)
      const radial = 1 - Math.abs(v - 0.5) * 2
      pos[i * 3] = Math.cos(a) * rr
      pos[i * 3 + 1] = Math.sin(a) * rr
      pos[i * 3 + 2] = (bell() - 0.5) * thick * 2
      const along = Math.pow(Math.sin(Math.PI * u), 1.15) * (0.3 + 0.7 * u)
      paint(col, i, c, bright * radial * along * (0.4 + Math.random() * 0.6))
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('color', new THREE.BufferAttribute(col, 4))
    return g
  }

  const innerDisc = new THREE.Points(orbit(9000, 2.02, 3.12, 0.12, 1, 1, pal.hues.inner, 1.75), dot(0.058, 0.95))
  const runner = new THREE.Points(arcBand(4200, 2.06, 3.06, 0.11, Math.PI * 0.72, pal.hues.runner, 4.2), dot(0.072, 1))
  const outerBand = new THREE.Points(orbit(5600, 4.64, 5.28, 0.22, 6, 0.62, pal.hues.outer, 3.0), dot(0.072, 0.95))
  rig.add(innerDisc, runner, outerBand)

  // Hairlines through the fields, so the mark stays legible inside the dust.
  // Built from the same feathered particles rather than from thin tori: a torus
  // arc ends on a flat cap, which is a 90-degree corner sitting in the middle of
  // a picture made entirely of curves.
  const wireIn = new THREE.Points(orbit(3400, 2.48, 2.64, 0.014, 1, 1, pal.hues.wireIn, 2.2), dot(0.03, 0.9))
  const outerWire = new THREE.Points(orbit(2100, 4.88, 5.02, 0.014, 6, 0.62, pal.hues.wireOut, 2.4), dot(0.028, 0.9))
  rig.add(wireIn, outerWire)

  // Radial streaks off the core, bright at the middle and spent well before the
  // rim, so the field has a centre and no ray ever ends on a visible point.
  const rays = (() => {
    const RAYS = 58
    const PER = 48
    const count = RAYS * PER
    const pos = new Float32Array(count * 3)
    const col = new Float32Array(count * 4)
    const c = new THREE.Color(pal.hues.rays)
    let i = 0
    for (let k = 0; k < RAYS; k++) {
      const a = Math.random() * Math.PI * 2
      const spread = 0.005 + Math.random() * 0.014
      const reach = 3.2 + Math.random() * 2.8
      const gain = 0.3 + Math.random() * 0.7
      for (let j = 0; j < PER; j++) {
        const t = j / (PER - 1)
        const rr = 0.5 + Math.pow(t, 1.3) * reach
        const aa = a + (bell() - 0.5) * spread * rr
        pos[i * 3] = Math.cos(aa) * rr
        pos[i * 3 + 1] = Math.sin(aa) * rr
        pos[i * 3 + 2] = (bell() - 0.5) * 0.06 * rr
        const fade = Math.pow(1 - t, 2.2) * Math.min(1, t * 8)
        paint(col, i, c, 2.6 * gain * fade)
        i++
      }
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('color', new THREE.BufferAttribute(col, 4))
    return new THREE.Points(g, dot(0.038, 0.7))
  })()
  rig.add(rays)

  // The core: the thing the orbits are about, and the scene's own light. Several
  // wide faint camera-facing sprites rather than one bright one — each stays well
  // under the clipping point, so the core reads as bright without any radius at
  // which the additive sum saturates and draws itself an edge. Inked, the same
  // stack reads as a bloom of shadow: each layer lerps the ground a little
  // further towards the deepest hue, and nothing ever draws an edge either.
  for (const [scale, op, hex] of pal.core) {
    const sp = new THREE.Sprite(
      new THREE.SpriteMaterial({
        map: glowTex,
        color: hex,
        transparent: true,
        opacity: op,
        blending: pal.additive ? THREE.AdditiveBlending : THREE.NormalBlending,
        depthWrite: false,
      }),
    )
    sp.scale.set(scale, scale, 1)
    rig.add(sp)
  }

  scene.add(new THREE.HemisphereLight(pal.lights.sky, pal.lights.below, pal.lights.hemi))
  const key = new THREE.DirectionalLight(0xffffff, pal.lights.key)
  key.position.set(7, 10, 8)
  scene.add(key)
  const fill = new THREE.PointLight(pal.lights.fill, pal.lights.fillPower, 30)
  fill.position.set(-5.5, 1.5, 5)
  scene.add(fill)
  const warm = new THREE.PointLight(pal.lights.warm, pal.lights.warmPower, 28)
  warm.position.set(6, -1.5, -3)
  scene.add(warm)
  const rim = new THREE.DirectionalLight(0x88aaff, pal.lights.rim)
  rim.position.set(-9, 3, -8)
  scene.add(rim)

  // Dust, so the depth is visible.
  const N = 1100
  const dustPos = new Float32Array(N * 3)
  for (let i = 0; i < N; i++) {
    dustPos[i * 3] = (Math.random() - 0.5) * 60
    dustPos[i * 3 + 1] = (Math.random() - 0.5) * 28 - 2
    dustPos[i * 3 + 2] = (Math.random() - 0.5) * 50 - 6
  }
  const dustGeo = new THREE.BufferGeometry()
  dustGeo.setAttribute('position', new THREE.BufferAttribute(dustPos, 3))
  const dust = new THREE.Points(
    dustGeo,
    new THREE.PointsMaterial({ color: pal.dust[0], size: 0.045, transparent: true, opacity: pal.dust[1], depthWrite: false }),
  )
  scene.add(dust)

  const resize = () => {
    const cw = el.clientWidth
    const ch = el.clientHeight
    if (!cw || !ch) return
    renderer.setSize(cw, ch)
    camera.aspect = cw / ch
    camera.updateProjectionMatrix()
  }
  const ro = new ResizeObserver(resize)
  ro.observe(el)

  const pointer = { x: 0, y: 0 }
  const target = { x: 0, y: 0 }
  const onMove = (e: PointerEvent) => {
    const r = stage!.getBoundingClientRect()
    target.x = ((e.clientX - r.left) / r.width - 0.5) * 2
    target.y = ((e.clientY - r.top) / r.height - 0.5) * 2
  }
  if (stage && !reduce) stage.addEventListener('pointermove', onMove)

  // Off screen it does not run. The hero is the top of a long page and a reader
  // spends most of their time below it.
  let visible = true
  const io = new IntersectionObserver((es) => {
    for (const e of es) visible = e.isIntersecting
  }, { threshold: 0 })
  io.observe(el)

  let dead = false
  let raf = 0

  const dispose = () => {
    dead = true
    if (raf) cancelAnimationFrame(raf)
    ro.disconnect()
    io.disconnect()
    if (stage) stage.removeEventListener('pointermove', onMove)
    // The GPU side of ~27k points across eight buffers, released by hand:
    // renderer.dispose() frees the context's own resources and nothing that was
    // uploaded through it. This used to run once, at unmount, where the leak
    // died with the page. 040 put an appearance switch in the footer of the
    // same page, and the palette is baked into every buffer — so the scene is
    // now rebuilt on demand, and a leak per rebuild is a leak per click.
    scene.traverse((o) => {
      const m = o as unknown as { geometry?: { dispose(): void }; material?: { dispose(): void } | { dispose(): void }[] }
      m.geometry?.dispose()
      if (Array.isArray(m.material)) for (const one of m.material) one.dispose()
      else m.material?.dispose()
    })
    glowTex.dispose()
    renderer.dispose()
    renderer.domElement.remove()
  }

  if (reduce) {
    renderer.render(scene, camera)
    return { dispose }
  }

  const t0 = performance.now()
  const tick = () => {
    if (dead) return
    raf = requestAnimationFrame(tick)
    if (!visible) return
    const t = (performance.now() - t0) / 1000
    // Differential rotation: the inner orbit is quicker than the outer one, and
    // the runner quicker again. The rig only breathes.
    innerDisc.rotation.z = t * 0.3
    wireIn.rotation.z = t * 0.3
    runner.rotation.z = t * 0.82
    outerBand.rotation.z = -t * 0.085
    outerWire.rotation.z = -t * 0.085
    rays.rotation.z = t * 0.045
    rig.rotation.y = Math.sin(t * 0.075) * 0.24
    rig.rotation.x = -1.02 + Math.sin(t * 0.05) * 0.07
    dust.rotation.y = t * 0.012
    pointer.x += (target.x - pointer.x) * 0.045
    pointer.y += (target.y - pointer.y) * 0.045
    camera.position.x = 0.4 + pointer.x * 1.6 + Math.sin(t * 0.18) * 0.3
    camera.position.y = 1.05 - pointer.y * 0.75 + Math.sin(t * 0.24) * 0.18
    camera.lookAt(0, 0.1, 0)
    renderer.render(scene, camera)
  }
  tick()
  return { dispose }
}
