// Password strength estimate for the "Create your account" form. It is a
// guide for the user, not a policy — the server enforces its own minimum.
// Scores 0–4 roughly follow zxcvbn's buckets using a cheap entropy model
// with penalties for the patterns people actually use.

export interface Strength {
  score: 0 | 1 | 2 | 3 | 4
  label: 'Too short' | 'Weak' | 'Fair' | 'Good' | 'Strong'
  /** One short, actionable hint (empty when strong). */
  hint: string
  bits: number
}

const COMMON = [
  'password',
  'passw0rd',
  'qwerty',
  'letmein',
  'welcome',
  'admin',
  'iloveyou',
  'monkey',
  'dragon',
  'football',
  'baseball',
  'master',
  'shadow',
  'sunshine',
  'princess',
  'abc123',
  '123456',
  'relay',
  'trustno1',
  'secret',
  'login',
  'changeme',
]
const SEQUENCES = ['abcdefghijklmnopqrstuvwxyz', '0123456789', 'qwertyuiop', 'asdfghjkl', 'zxcvbnm']

/** Estimate password strength. `context` words (username, hostname) count as common. */
export function passwordStrength(pw: string, context: string[] = []): Strength {
  if (pw.length < 8) return { score: 0, label: 'Too short', hint: 'Use at least 8 characters.', bits: 0 }
  let pool = 0
  if (/[a-z]/.test(pw)) pool += 26
  if (/[A-Z]/.test(pw)) pool += 26
  if (/[0-9]/.test(pw)) pool += 10
  if (/[^a-zA-Z0-9]/.test(pw)) pool += 33
  const unique = new Set(pw).size
  // Effective length: repeated characters add little.
  const effLen = Math.min(pw.length, unique * 2.2)
  let bits = effLen * Math.log2(Math.max(pool, 2))

  const lower = pw.toLowerCase()
  let hint = ''
  const words = [...COMMON, ...context.map((w) => w.toLowerCase()).filter((w) => w.length >= 3)]
  for (const w of words) {
    if (lower.includes(w)) {
      bits -= w.length * 3.3
      hint = 'Avoid common words and your username.'
    }
  }
  for (const seq of SEQUENCES) {
    for (let i = 0; i + 4 <= seq.length; i++) {
      if (lower.includes(seq.slice(i, i + 4))) {
        bits -= 10
        hint ||= 'Avoid sequences like "abcd" or "1234".'
        break
      }
    }
  }
  if (/(.)\1{2,}/.test(pw)) {
    bits -= 8
    hint ||= 'Avoid repeated characters.'
  }
  bits = Math.max(0, Math.round(bits))

  let score: Strength['score']
  if (bits < 36) score = 1
  else if (bits < 52) score = 2
  else if (bits < 68) score = 3
  else score = 4
  const labels = ['Too short', 'Weak', 'Fair', 'Good', 'Strong'] as const
  if (!hint && score < 4)
    hint = pw.length < 14 ? 'Longer is stronger — try a short phrase.' : 'Mix in another word.'
  if (score === 4) hint = ''
  return { score, label: labels[score], hint, bits }
}
