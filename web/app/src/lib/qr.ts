/** otpauth URIs are long; a QR image is rendered by the browser-side
 * `qrcode` library in the Account page. This helper only builds the
 * download for recovery codes. */
export function recoveryCodesFile(codes: string[], email: string): string {
  const lines = [`Camhive recovery codes for ${email}`, `Each code works once. Keep this file somewhere safe.`, "", ...codes];
  return lines.join("\n") + "\n";
}
