/**
 * PKCE (Proof Key for Code Exchange) utilities for OAuth 2.0 authorization code flow.
 * Implements RFC 7636 sections 4.1 and 4.2.
 */

const VERIFIER_CHARSET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~'
const VERIFIER_LENGTH = 64

/**
 * Generate a cryptographically random code verifier (RFC 7636 4.1).
 * Returns a 64-character string from the unreserved character set.
 */
export function generateCodeVerifier(): string {
  const array = new Uint8Array(VERIFIER_LENGTH)
  crypto.getRandomValues(array)
  return Array.from(array, (byte) => VERIFIER_CHARSET[byte % VERIFIER_CHARSET.length]).join('')
}

/**
 * Generate a S256 code challenge from a code verifier (RFC 7636 4.2).
 * challenge = BASE64URL(SHA256(verifier))
 */
export async function generateCodeChallenge(verifier: string): Promise<string> {
  const encoder = new TextEncoder()
  const data = encoder.encode(verifier)
  const digest = await crypto.subtle.digest('SHA-256', data)
  return base64UrlEncode(digest)
}

/**
 * Base64 URL encoding without padding (RFC 4648 section 5).
 */
function base64UrlEncode(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/**
 * Generate a random state parameter for CSRF protection.
 */
export function generateState(): string {
  const array = new Uint8Array(32)
  crypto.getRandomValues(array)
  return base64UrlEncode(array.buffer)
}
