import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

/**
 * `cn` — conditional classname composer.
 *
 * Standard shadcn/ui utility. Merges Tailwind classes so later
 * values override earlier ones on conflicts (e.g. `p-2 p-4` → `p-4`).
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}

/**
 * Generate a stable id from a string seed. Used by form label/input
 * pairings where we want deterministic ids for test selectors.
 */
export function stableId(seed: string): string {
  return seed
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
}
