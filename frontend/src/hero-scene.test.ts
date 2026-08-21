// The hero scene has two palettes (040), and the guard that matters is that
// nothing paints outside them. A literal left behind in the geometry is a
// colour that cannot follow the appearance — the exact bug 040 fixed in the
// stylesheet — and no browser test can see it: the scene is WebGL, the test
// harness has no GPU, and a canvas is opaque to a computed-style probe.
//
// So this reads the source. It is the same instrument latere-ui uses to keep an
// `@import` out of a shipped stylesheet, for the same reason: some properties
// are about how a file is written, not about what it renders.
import { describe, expect, it } from 'bun:test'
import { readFileSync } from 'node:fs'

const src = readFileSync(new URL('./hero-scene.ts', import.meta.url), 'utf8')

// The two palette literals, sliced out by their declarations.
const paletteOf = (name: string) => {
  const at = src.indexOf(`const ${name}: Palette = {`)
  expect(at).toBeGreaterThan(-1)
  const end = src.indexOf('\n}\n', at)
  return src.slice(at, end)
}

describe('the hero scene', () => {
  it('paints from a palette and nowhere else', () => {
    const outside = src.replace(paletteOf('DARK'), '').replace(paletteOf('LIGHT'), '')
    // 0xffffff is the key light, white in both states because a white key is
    // what "unlit" means for the one lit surface in the scene, and 0x88aaff is
    // the rim, which the palette scales rather than recolours.
    const allowed = new Set(['0xffffff', '0x88aaff'])
    const stray = (outside.match(/0x[0-9a-f]{6}/g) ?? []).filter((h) => !allowed.has(h))
    expect(stray).toEqual([])
  })

  it('gives both states the same vocabulary', () => {
    const keys = (block: string) => (block.match(/^ {2}([a-zA-Z]+):/gm) ?? []).map((k) => k.trim())
    expect(keys(paletteOf('LIGHT'))).toEqual(keys(paletteOf('DARK')))
  })

  it('adds light in the dark and inks on paper', () => {
    expect(paletteOf('DARK')).toContain('additive: true')
    expect(paletteOf('LIGHT')).toContain('additive: false')
    // Blending, tone mapping and the colour buffers all branch on that one
    // flag; nothing inside the builder may branch on the theme name itself.
    const body = src.slice(src.indexOf('export async function mountHeroScene'))
    expect(body.split("=== 'light'").length - 1).toBe(1)
  })

  it('rebuilds for the theme it is handed', () => {
    expect(src).toContain("theme: HeroTheme = 'dark'")
    expect(src).toContain("const pal = theme === 'light' ? LIGHT : DARK")
  })

  it('frees what it uploaded, because a theme switch rebuilds it', () => {
    const dispose = src.slice(src.indexOf('const dispose = () => {'))
    for (const call of ['geometry?.dispose()', 'glowTex.dispose()', 'renderer.dispose()']) {
      expect(dispose).toContain(call)
    }
  })
})
