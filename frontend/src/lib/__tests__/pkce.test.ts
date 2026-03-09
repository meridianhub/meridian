import { describe, it, expect } from 'vitest'
import { generateCodeVerifier, generateCodeChallenge, generateState } from '../pkce'

describe('PKCE utilities', () => {
  describe('generateCodeVerifier', () => {
    it('generates a 64-character string', () => {
      const verifier = generateCodeVerifier()
      expect(verifier).toHaveLength(64)
    })

    it('uses only unreserved characters (RFC 7636 4.1)', () => {
      const verifier = generateCodeVerifier()
      expect(verifier).toMatch(/^[A-Za-z0-9\-._~]+$/)
    })

    it('generates unique verifiers', () => {
      const v1 = generateCodeVerifier()
      const v2 = generateCodeVerifier()
      expect(v1).not.toEqual(v2)
    })
  })

  describe('generateCodeChallenge', () => {
    it('produces a base64url string without padding', async () => {
      const verifier = generateCodeVerifier()
      const challenge = await generateCodeChallenge(verifier)
      expect(challenge).toMatch(/^[A-Za-z0-9\-_]+$/)
      expect(challenge).not.toContain('=')
    })

    it('produces a consistent challenge for the same verifier', async () => {
      const verifier = 'test-verifier-for-consistency'
      const c1 = await generateCodeChallenge(verifier)
      const c2 = await generateCodeChallenge(verifier)
      expect(c1).toEqual(c2)
    })

    it('produces different challenges for different verifiers', async () => {
      const c1 = await generateCodeChallenge('verifier-one')
      const c2 = await generateCodeChallenge('verifier-two')
      expect(c1).not.toEqual(c2)
    })
  })

  describe('generateState', () => {
    it('generates a base64url string', () => {
      const state = generateState()
      expect(state).toMatch(/^[A-Za-z0-9\-_]+$/)
    })

    it('generates unique states', () => {
      const s1 = generateState()
      const s2 = generateState()
      expect(s1).not.toEqual(s2)
    })
  })
})
