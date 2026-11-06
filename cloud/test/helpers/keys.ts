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
