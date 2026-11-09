// Unbound says what an unbound script's endpoint card can do about it.
export function Unbound({ isAdmin, isForm }: { isAdmin: boolean; isForm: boolean }) {
  let text = "Only an administrator can bind a script to an endpoint.";
  if (!isAdmin && isForm) {
    text = "This form is not published. Preview it first, then publish it for the signed-in people of this deployment.";
  }
  if (isAdmin) {
    text = isForm
      ? "This form answers no requests. Bind it to an endpoint to show it at /forms/{name} and store each accepted submission."
      : "This script answers no HTTP requests. Bind it to an endpoint to run handle(req) on each request to /endpoints/{name}.";
  }
  return <p className="text-sm text-muted-foreground">{text}</p>;
}
