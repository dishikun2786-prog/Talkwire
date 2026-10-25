import jwt from "jsonwebtoken";

/** The role claim of the tokens the IM server and the host exchange. */
export const INTERNAL_SERVICE_ROLE = "internal_service";

/**
 * Signs an internal_service token: HS256 with the secret both sides share,
 * valid for ttlSeconds (the IM server signs one-minute tokens).
 */
export function signInternalToken(secret: string, ttlSeconds = 60): string {
  return jwt.sign({ role: INTERNAL_SERVICE_ROLE }, secret, { algorithm: "HS256", expiresIn: ttlSeconds });
}

/** Whether token is a valid, unexpired HS256 internal_service token. */
export function verifyInternalToken(secret: string, token: string): boolean {
  if (!token) {
    return false;
  }
  try {
    const claims = jwt.verify(token, secret, { algorithms: ["HS256"] });
    return typeof claims === "object" && claims !== null && claims.role === INTERNAL_SERVICE_ROLE;
  } catch {
    return false;
  }
}

/** The token of an "Authorization: Bearer <token>" header, or "". */
export function bearerToken(header: string | undefined): string {
  return header?.startsWith("Bearer ") ? header.slice(7) : "";
}
