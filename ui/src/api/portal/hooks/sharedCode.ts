// SharedFunction is one function of a saved source that already exists
// elsewhere (#2273): in a library, with the load line that replaces the copy,
// or in other scripts, by name. It never carries code.
export interface SharedFunction {
  function: string;
  library?: string;
  version?: number;
  library_function?: string;
  load?: string;
  scripts?: string[];
  next: string;
}
