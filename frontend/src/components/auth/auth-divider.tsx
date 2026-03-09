export function AuthDivider() {
  return (
    <div className="relative">
      <div className="absolute inset-0 flex items-center">
        <span className="w-full border-t border-muted" />
      </div>
      <div className="relative flex justify-center text-xs uppercase">
        <span className="bg-background px-2 text-muted-foreground">Or continue with</span>
      </div>
    </div>
  )
}
