import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Render an unknown error value as a user-readable message. Use this in
 * place of `(err as Error).message` — react-query and apiFetch can throw
 * non-Error values, and the type assertion silently NPEs at runtime.
 */
export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return String(err);
}
