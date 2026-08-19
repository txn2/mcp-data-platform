import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
  /** What to show instead of the children when they fail. */
  fallback: ReactNode;
  /**
   * Changing this resets the boundary. Pass the route (or whatever identifies
   * what is being rendered) so navigating away from a broken page clears the
   * error instead of pinning it there until a reload.
   */
  resetKey?: string;
}

interface State {
  failed: boolean;
}

/**
 * Catches a render failure in its subtree and shows the fallback instead of
 * letting React unmount the whole application.
 *
 * The failure this exists for is a chunk that does not arrive. Both the portal
 * and the public share viewer load their pages and renderers on demand
 * (#1351, #1355), and a rejected `import()` is not something `<Suspense>`
 * catches — it propagates to the root, where React's answer is to unmount
 * everything and leave a blank document. That is reachable in ordinary
 * operation, not just in a browser with no network: chunk filenames carry a
 * content hash and each build embeds its own, so a tab left open across a
 * deploy asks a replica for a hash it does not have and is answered 404.
 *
 * Reloading is the fix for that case — it fetches the new document, which
 * names the new chunks — so the fallback's job is to say so rather than to
 * retry silently against a name that will never resolve.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  componentDidUpdate(prev: Props) {
    if (this.state.failed && prev.resetKey !== this.props.resetKey) {
      this.setState({ failed: false });
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Left visible in the console on purpose: the fallback tells the reader
    // what to do, and this is what tells whoever they report it to what broke.
    console.error("render failed", error, info.componentStack);
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}
