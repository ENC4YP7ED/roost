/**
 * Browser-side WebAuthn (passkey) helpers.
 *
 * The panel serialises the server's credential options as JSON, where every
 * binary field (challenge, user id, credential ids) is base64url text. The
 * `navigator.credentials` API instead wants ArrayBuffers going in and gives
 * ArrayBuffers coming out — so these helpers convert both ways and shape the
 * assertion/attestation back into the JSON the server expects.
 */

function fromB64url(s: string): ArrayBuffer {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const b64 = (s + pad).replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64);
  const buf = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
  return buf.buffer;
}

function toB64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function passkeysSupported(): boolean {
  return typeof window !== "undefined" && !!window.PublicKeyCredential && !!navigator.credentials;
}

/** Run the create() ceremony from server options; returns the attestation JSON. */
export async function createPasskey(publicKey: any): Promise<unknown> {
  const opts: any = { ...publicKey };
  opts.challenge = fromB64url(publicKey.challenge);
  opts.user = { ...publicKey.user, id: fromB64url(publicKey.user.id) };
  if (publicKey.excludeCredentials) {
    opts.excludeCredentials = publicKey.excludeCredentials.map((c: any) => ({ ...c, id: fromB64url(c.id) }));
  }
  const cred = (await navigator.credentials.create({ publicKey: opts })) as PublicKeyCredential;
  if (!cred) throw new Error("Passkey creation was cancelled.");
  const res = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: toB64url(res.clientDataJSON),
      attestationObject: toB64url(res.attestationObject),
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}

/** Run the get() ceremony for a (discoverable) login; returns the assertion JSON. */
export async function getPasskey(publicKey: any): Promise<unknown> {
  const opts: any = { ...publicKey };
  opts.challenge = fromB64url(publicKey.challenge);
  if (publicKey.allowCredentials) {
    opts.allowCredentials = publicKey.allowCredentials.map((c: any) => ({ ...c, id: fromB64url(c.id) }));
  }
  const cred = (await navigator.credentials.get({ publicKey: opts })) as PublicKeyCredential;
  if (!cred) throw new Error("Sign-in was cancelled.");
  const res = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: toB64url(res.clientDataJSON),
      authenticatorData: toB64url(res.authenticatorData),
      signature: toB64url(res.signature),
      userHandle: res.userHandle ? toB64url(res.userHandle) : null,
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}
