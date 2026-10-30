import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Copies text to the clipboard. The Clipboard API only exists on HTTPS/localhost,
 * so self-hosted plain-HTTP installs fall back to a temporary textarea.
 */
export async function copyText(text: string) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return
  }
  // Inside a modal dialog, focus is trapped: the textarea must live in the dialog.
  const host = document.activeElement?.closest<HTMLElement>('[role="dialog"],[role="alertdialog"]') ?? document.body
  const textarea = document.createElement("textarea")
  textarea.value = text
  textarea.setAttribute("readonly", "")
  textarea.style.position = "fixed"
  textarea.style.opacity = "0"
  host.appendChild(textarea)
  textarea.select()
  try {
    if (!document.execCommand("copy")) throw new Error("Copy failed")
  } finally {
    textarea.remove()
  }
}
