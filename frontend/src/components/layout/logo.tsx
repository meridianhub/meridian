/**
 * The butterfly ledger mark. The body is the meridian line; left wings
 * outlined = debit, right wings filled credit green = credit. Body and
 * left wings take currentColor; the right wings follow --primary so the
 * green swaps to the engine green on dark surfaces.
 */
export function MeridianMark({ className }: { className?: string }) {
  return (
    <svg
      width="30"
      height="30"
      viewBox="0 0 50 50"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      className={className}
    >
      <line x1="25" y1="7" x2="25" y2="43" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" />
      <path d="M22.5 23 C15 11, 5.5 7.5, 4.5 13 C3.5 19.5, 11 26, 22.5 27.5 Z" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinejoin="round" />
      <path d="M22.5 30.5 C15.5 30, 9.5 34, 10.5 38.5 C11.5 42.5, 19 41.5, 22.5 34.5 Z" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinejoin="round" />
      <path d="M27.5 23 C35 11, 44.5 7.5, 45.5 13 C46.5 19.5, 39 26, 27.5 27.5 Z" fill="var(--primary)" stroke="var(--primary)" strokeWidth="2.2" strokeLinejoin="round" />
      <path d="M27.5 30.5 C34.5 30, 40.5 34, 39.5 38.5 C38.5 42.5, 31 41.5, 27.5 34.5 Z" fill="var(--primary)" stroke="var(--primary)" strokeWidth="2.2" strokeLinejoin="round" />
    </svg>
  )
}
