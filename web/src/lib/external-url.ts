/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

/**
 * Reject non-navigable schemes (e.g. javascript:, data:) and relative URLs.
 * Only absolute http/https URLs are allowed for server-provided navigation
 * targets (payment links, checkout URLs, upstream resource URLs).
 */
export function isSafeExternalHttpUrl(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) {
    return false
  }
  try {
    const url = new URL(trimmed)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

/**
 * Open a server-provided URL in a new tab without granting the target a
 * window.opener reference. Returns false (and does not navigate) when the
 * URL is not an absolute http/https URL.
 */
export function openExternalUrl(url: string): boolean {
  if (!isSafeExternalHttpUrl(url)) {
    return false
  }
  window.open(url.trim(), '_blank', 'noopener,noreferrer')
  return true
}

/**
 * Same-window redirect for flows where a popup would be blocked (the user
 * gesture is lost across an await). Returns false when the URL is not an
 * absolute http/https URL.
 */
export function redirectToExternalUrl(url: string): boolean {
  if (!isSafeExternalHttpUrl(url)) {
    return false
  }
  window.location.href = url.trim()
  return true
}
