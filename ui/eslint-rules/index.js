// Local ESLint rules for the portal source.
//
// no-bare-name-sort (#2389): a list of named things is shown in one order
// everywhere, the one `compareNames`/`byName` in src/lib/sortByName.ts give
// (case-insensitive, natural numbers). A bare `.sort()` orders by code unit
// ("Zeta" before "alpha", "conn-10" before "conn-2") and `localeCompare`
// without the shared collator options disagrees with it, so both are refused.
// A sort over data that is not a name (numbers, ISO timestamps, keys joined
// for a stable signature) passes an explicit comparator instead, such as
// `compareOrdinal` or `(a, b) => a - b`.

const MESSAGE_SORT =
  "A bare .sort() orders by code unit. Use compareNames/byName from @/lib/sortByName for names, or pass an explicit comparator (compareOrdinal, (a, b) => a - b) for other data.";
const MESSAGE_LOCALE =
  "localeCompare disagrees with the portal's name order. Use compareNames/byName from @/lib/sortByName for names, or compareOrdinal for keys and timestamps.";

function calleeProperty(node) {
  const callee = node.callee;
  if (callee.type !== "MemberExpression" || callee.computed) return null;
  return callee.property.type === "Identifier" ? callee.property.name : null;
}

const noBareNameSort = {
  meta: {
    type: "problem",
    docs: {
      description: "Refuse a bare .sort() and localeCompare; names sort through @/lib/sortByName.",
    },
    schema: [],
    messages: { bareSort: MESSAGE_SORT, localeCompare: MESSAGE_LOCALE },
  },
  create(context) {
    return {
      CallExpression(node) {
        const name = calleeProperty(node);
        if (name === "sort" && node.arguments.length === 0) {
          context.report({ node, messageId: "bareSort" });
        } else if (name === "localeCompare") {
          context.report({ node, messageId: "localeCompare" });
        }
      },
    };
  },
};

export default {
  meta: { name: "eslint-plugin-local" },
  rules: { "no-bare-name-sort": noBareNameSort },
};
