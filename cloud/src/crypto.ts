/** Base64 (standard alphabet, padded) and Ed25519 helpers shared by the node API and the hub. */

export function decodeBase64(value: string): Uint8Array | null {
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(value) || value.length % 4 !== 0) return null;
  try {
    const binary = atob(value);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes;
  } catch {
    return null;
  }
}

export function encodeBase64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

/** A raw Ed25519 public key is exactly 32 bytes. */
export const ED25519_PUBLIC_KEY_BYTES = 32;
export const ED25519_SIGNATURE_BYTES = 64;

/** Returns the raw key bytes when `publicKey` is the base64 of a 32-byte Ed25519 key, else null. */
export function parsePublicKey(publicKey: string): Uint8Array | null {
  const bytes = decodeBase64(publicKey);
  return bytes && bytes.length === ED25519_PUBLIC_KEY_BYTES ? bytes : null;
}

/** Verifies an Ed25519 signature (base64) over the UTF-8 bytes of `message` with a base64 raw public key. */
export async function verifyEd25519(publicKey: string, message: string, signature: string): Promise<boolean> {
  const keyBytes = parsePublicKey(publicKey);
  const signatureBytes = decodeBase64(signature);
  if (!keyBytes || !signatureBytes || signatureBytes.length !== ED25519_SIGNATURE_BYTES) return false;
  try {
    const key = await crypto.subtle.importKey("raw", keyBytes, { name: "Ed25519" }, false, ["verify"]);
    return await crypto.subtle.verify("Ed25519", key, signatureBytes, new TextEncoder().encode(message));
  } catch {
    return false;
  }
}

/** First 16 hex characters of SHA-256 over the raw key, grouped by 4 with ":" (as `wrekin status` prints it). */
export async function fingerprint(publicKey: string): Promise<string> {
  const keyBytes = parsePublicKey(publicKey);
  if (!keyBytes) return "invalid-key";
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", keyBytes));
  const hex = Array.from(digest.subarray(0, 8), (b) => b.toString(16).padStart(2, "0")).join("");
  return hex.match(/.{4}/g)?.join(":") ?? hex;
}

/** 26 lowercase base32 characters (RFC 4648 alphabet) of 16 random bytes: ids such as `node_…`. */
export function randomId(prefix: string): string {
  const alphabet = "abcdefghijklmnopqrstuvwxyz234567";
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  let bits = 0;
  let value = 0;
  let out = "";
  for (const byte of bytes) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      out += alphabet[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) out += alphabet[(value << (5 - bits)) & 31];
  return `${prefix}_${out}`;
}
