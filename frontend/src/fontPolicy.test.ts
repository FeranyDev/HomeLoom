import { describe, expect, it } from 'vitest'
import indexHtml from '../index.html?raw'

describe('font policy', () => {
	it('loads the pinned MiSans webfont stylesheet', () => {
		expect(indexHtml).toContain('https://cdn.jsdelivr.net/npm/misans-webfont@4.3.1/misans-style.min.css')
	})
})
