import { describe, expect, it } from 'vitest'
import { episodeStill, firstImage } from '@/lib/images'

describe('firstImage', () => {
  it('reads the contract array shape the API now serves', () => {
    // Exactly what GET /v1/movies/{id} returns: every key of the type present,
    // each an array, "absent" expressed as an empty array.
    const images = {
      banner: [],
      clearart: [],
      fanart: ['https://image.tmdb.org/t/p/w780/b.jpg'],
      logo: [],
      poster: ['https://image.tmdb.org/t/p/w500/p.jpg'],
      thumb: [],
    }
    expect(firstImage(images, 'fanart', 'poster')).toBe(
      'https://image.tmdb.org/t/p/w780/b.jpg'
    )
    expect(firstImage(images, 'poster')).toBe(
      'https://image.tmdb.org/t/p/w500/p.jpg'
    )
    // logo is present but empty: a caller asking for it gets nothing back rather
    // than the '' that `<img src="">` would request from the page itself.
    expect(firstImage(images, 'logo')).toBeUndefined()
  })

  it('falls through to the next key when the first is empty', () => {
    expect(
      firstImage({ fanart: [], poster: ['p.jpg'] }, 'fanart', 'poster')
    ).toBe('p.jpg')
  })

  it('still reads the scalar shape a stored row can hold', () => {
    expect(
      firstImage({ poster: 'p.jpg', backdrop: 'b.jpg' }, 'fanart', 'poster')
    ).toBe('p.jpg')
  })

  it('skips empty strings inside an array', () => {
    expect(firstImage({ poster: ['', 'p.jpg'] }, 'poster')).toBe('p.jpg')
  })

  it('prefers a legacy backdrop over the poster, the way the old chain did', () => {
    // `backdrop` is the name a stored row carries for fanart, so a row holding one
    // and no fanart must still render it — the chain this replaced was
    // `backdrop ?? poster` — while a real fanart still wins over it.
    const hero = ['fanart', 'backdrop', 'poster'] as const
    expect(
      firstImage({ backdrop: ['b.jpg'], poster: ['p.jpg'] }, ...hero)
    ).toBe('b.jpg')
    expect(
      firstImage({ fanart: ['f.jpg'], backdrop: ['b.jpg'] }, ...hero)
    ).toBe('f.jpg')
    expect(firstImage({ poster: ['p.jpg'] }, ...hero)).toBe('p.jpg')
  })

  it('handles a missing or null images object', () => {
    expect(firstImage(undefined, 'poster')).toBeUndefined()
    expect(firstImage(null, 'poster')).toBeUndefined()
    expect(firstImage({}, 'poster')).toBeUndefined()
  })

  it('reads an episode still from screenshot, which is the contract name', () => {
    expect(episodeStill({ screenshot: ['still.jpg'] })).toBe('still.jpg')
    // The pre-contract names an older row carries.
    expect(episodeStill({ poster: ['p.jpg'] })).toBe('p.jpg')
    expect(episodeStill({ thumb: ['t.jpg'] })).toBe('t.jpg')
    expect(episodeStill({ screenshot: [] })).toBeUndefined()
  })
})
