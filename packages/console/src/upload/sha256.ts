// packages/console/src/upload/sha256.ts
//
// Per-chunk sha256, lowercase hex, computed with the WebCrypto SubtleCrypto
// digest. The FT-2 service recomputes the digest of each chunk body and
// rejects a mismatch (422), so this is the real integrity check on the wire,
// not a display affordance. SubtleCrypto is available in secure contexts
// (https or localhost); the console runs in one.

export async function sha256Hex(buf: ArrayBuffer): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", buf);
  const bytes = new Uint8Array(digest);
  let out = "";
  for (let i = 0; i < bytes.length; i++) {
    out += bytes[i].toString(16).padStart(2, "0");
  }
  return out;
}
