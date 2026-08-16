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
// no WebGL, no chunk, or a throw on any line leaves the caller with a dark slab
// and a page that reads exactly as designed.

// The slab's own colours. They are literal rather than tokens on purpose: the
// hero is a material, not a theme, and it is this dark in light mode too.
const VOID = 0x0a0b0d

export type HeroScene = { dispose: () => void }

// mount builds the scene inside `el` and starts it. `reduce` renders exactly one
// frame and never schedules another — the resting state of this figure is the
// figure, not a first frame of it. `stage` is the element pointer movement is
// measured against, which is the whole slab rather than the canvas, so the
// parallax follows the cursor over the copy as well.
export async function mountHeroScene(el: HTMLElement, stage: HTMLElement | null, reduce: boolean): Promise<HeroScene | null> {
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

  const w = el.clientWidth || 1200
  const h = el.clientHeight || 700
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
  renderer.setSize(w, h)
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  renderer.toneMappingExposure = 1.15
  renderer.setClearColor(VOID, 1)
  renderer.domElement.style.display = 'block'
  el.appendChild(renderer.domElement)

  const scene = new THREE.Scene()
  scene.fog = new THREE.FogExp2(VOID, 0.036)
  const camera = new THREE.PerspectiveCamera(38, w / h, 0.1, 300)
  camera.position.set(0.4, 1.05, 13.2)
  camera.lookAt(0, 0.1, 0)

  // Floor: a plane plus a grid, both eaten by fog, so the object sits in a place
  // rather than on a colour.
  const floor = new THREE.Mesh(
    new THREE.PlaneGeometry(300, 300),
    new THREE.MeshStandardMaterial({ color: 0x0d1014, roughness: 0.62, metalness: 0.28 }),
  )
  floor.rotation.x = -Math.PI / 2
  floor.position.y = -4.2
  scene.add(floor)
  const grid = new THREE.GridHelper(160, 80, 0x1c2836, 0x141b24)
  grid.position.y = -4.18
  const gridMat = grid.material as import('three').Material
  gridMat.transparent = true
  gridMat.opacity = 0.5
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
      opacity,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
      sizeAttenuation: true,
      fog: false,
    })

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
    const col = new Float32Array(count * 3)
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
      const b = bright * (0.12 + 0.88 * radial * along) * (0.35 + Math.random() * 0.65)
      col[i * 3] = c.r * b
      col[i * 3 + 1] = c.g * b
      col[i * 3 + 2] = c.b * b
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('color', new THREE.BufferAttribute(col, 3))
    return g
  }

  // The runner: the one arc that travels the closed loop, which is what "re-runs"
  // looks like. It reaches zero brightness at both ends rather than stopping at
  // full, and still leans forward, so the run keeps its direction without the
  // leading edge terminating on a straight cut.
  const arcBand = (count: number, rIn: number, rOut: number, thick: number, aSpan: number, hex: number, bright: number) => {
    const pos = new Float32Array(count * 3)
    const col = new Float32Array(count * 3)
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
      const b = bright * radial * along * (0.4 + Math.random() * 0.6)
      col[i * 3] = c.r * b
      col[i * 3 + 1] = c.g * b
      col[i * 3 + 2] = c.b * b
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('color', new THREE.BufferAttribute(col, 3))
    return g
  }

  const innerDisc = new THREE.Points(orbit(9000, 2.02, 3.12, 0.12, 1, 1, 0x3d86e0, 1.75), dot(0.058, 0.95))
  const runner = new THREE.Points(arcBand(4200, 2.06, 3.06, 0.11, Math.PI * 0.72, 0xbcd9fb, 4.2), dot(0.072, 1))
  const outerBand = new THREE.Points(orbit(5600, 4.64, 5.28, 0.22, 6, 0.62, 0xd2554f, 3.0), dot(0.072, 0.95))
  rig.add(innerDisc, runner, outerBand)

  // Hairlines through the fields, so the mark stays legible inside the dust.
  // Built from the same feathered particles rather than from thin tori: a torus
  // arc ends on a flat cap, which is a 90-degree corner sitting in the middle of
  // a picture made entirely of curves.
  const wireIn = new THREE.Points(orbit(3400, 2.48, 2.64, 0.014, 1, 1, 0x9cc4f2, 2.2), dot(0.03, 0.9))
  const outerWire = new THREE.Points(orbit(2100, 4.88, 5.02, 0.014, 6, 0.62, 0xe8a8a3, 2.4), dot(0.028, 0.9))
  rig.add(wireIn, outerWire)

  // Radial streaks off the core, bright at the middle and spent well before the
  // rim, so the field has a centre and no ray ever ends on a visible point.
  const rays = (() => {
    const RAYS = 58
    const PER = 48
    const count = RAYS * PER
    const pos = new Float32Array(count * 3)
    const col = new Float32Array(count * 3)
    const c = new THREE.Color(0x7fb6f0)
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
        const b = 2.6 * gain * fade
        col[i * 3] = c.r * b
        col[i * 3 + 1] = c.g * b
        col[i * 3 + 2] = c.b * b
        i++
      }
    }
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3))
    g.setAttribute('color', new THREE.BufferAttribute(col, 3))
    return new THREE.Points(g, dot(0.038, 0.7))
  })()
  rig.add(rays)

  // The core: the thing the orbits are about, and the scene's own light. Several
  // wide faint camera-facing sprites rather than one bright one — each stays well
  // under the clipping point, so the core reads as bright without any radius at
  // which the additive sum saturates and draws itself an edge.
  for (const [scale, op, hex] of [
    [7.5, 0.1, 0x5f93d8],
    [4.6, 0.13, 0x86b4ec],
    [2.7, 0.16, 0xa8cbf5],
    [1.5, 0.2, 0xd6e8fc],
    [0.75, 0.26, 0xffffff],
  ] as const) {
    const sp = new THREE.Sprite(
      new THREE.SpriteMaterial({ map: glowTex, color: hex, transparent: true, opacity: op, blending: THREE.AdditiveBlending, depthWrite: false }),
    )
    sp.scale.set(scale, scale, 1)
    rig.add(sp)
  }

  scene.add(new THREE.HemisphereLight(0x9fc4ee, 0x08090b, 0.5))
  const key = new THREE.DirectionalLight(0xffffff, 2.1)
  key.position.set(7, 10, 8)
  scene.add(key)
  const fill = new THREE.PointLight(0x2369bd, 40, 30)
  fill.position.set(-5.5, 1.5, 5)
  scene.add(fill)
  const warm = new THREE.PointLight(0xa9373b, 26, 28)
  warm.position.set(6, -1.5, -3)
  scene.add(warm)
  const rim = new THREE.DirectionalLight(0x88aaff, 1.1)
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
    new THREE.PointsMaterial({ color: 0x9fb4d0, size: 0.045, transparent: true, opacity: 0.55, depthWrite: false }),
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
