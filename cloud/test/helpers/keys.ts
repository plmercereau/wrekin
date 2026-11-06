import { encodeBase64 } from "../../src/crypto";

/** An Ed25519 key pair generated with WebCrypto, as a node would hold it. */
export async function generateNodeKey() {
  const pair = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"])) as CryptoKeyPair;
  const raw = new Uint8Array((await crypto.subtle.exportKey("raw", pair.publicKey)) as ArrayBuffer);
  return {
    publicKey: encodeBase64(raw),
    async sign(message: string): Promise<string> {
      const sig = await crypto.subtle.sign("Ed25519", pair.privateKey, new TextEncoder().encode(message));
      return encodeBase64(new Uint8Array(sig));
    },
  };
}

const BASE64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/**
 * The same 32 bytes under another base64 spelling: the 43rd character of a 44-character
 * encoding carries two padding bits a lenient decoder ignores, so flipping its lowest bit
 * changes the string and not the key (`…Q=` and `…R=` are the same key).
 */
export function alternateSpelling(publicKey: string): string {
  if (publicKey.length !== 44 || !publicKey.endsWith("=")) throw new Error("expected the base64 of 32 bytes");
  const index = BASE64.indexOf(publicKey[42] ?? "");
  if (index < 0) throw new Error("not base64");
  return `${publicKey.slice(0, 42)}${BASE64[index ^ 1]}=`;
}
