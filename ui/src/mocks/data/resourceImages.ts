/**
 * Image bytes for the mock resource library.
 *
 * The library's image sections are drawn from the resource content endpoint
 * itself, there being no stored thumbnail for a resource, so a fixture library
 * whose content endpoint answers with a string renders a grid of broken tiles.
 * These are small PNG gradients, scaled up by the tile's own `object-cover`.
 */
const PNG: Record<string, string> = {
  slate:
    "iVBORw0KGgoAAAANSUhEUgAAABAAAAAMCAIAAADkharWAAAB40lEQVR42iVRWTPCURy9X8cbD3YilUroL4oi7Wmn0qJItlSkhBTG3mQbw2AMhhfLEw+MsXwb52bmzp25Z875nXN+l5Sw1GUCW5XEx1KMN/VPc9SRJtVMQ+9EXfcowAqxu7zNVSEerukaAcg3zJNSvrWqw0vZqghXG+NqolSgCNfKghBUMsOVjKfIDjfr59odGYI3Sz6Owc26OCCeNgYfILXSQLXEB02NNAA9Txdrs69I/TukXh7iqGfhJTAu4OZqo2zlFEAIMBg+YANstS51ebf6Jo4JZRsTQlNKaEpSgSba2DdZ3xOqk42iBpIAEVnSnZ7N3vCRLn5FMBhvkTUtNKcgQDYIEAkmaMnVzLaYFyXuDUXoQBu7NKcfCLwQrtW2DBN0QHUIMBg32AA7XOvysYImemFafHDkXkj74CoOFQwkebo4VoQObOU0kiAk48z1BPPqyLkpdQ+2a/OVMM41CETWJWTDijgq+g9oCb14KNsd2FPNnA0k7xzZF/fWm3fvncCRCixpVOcV/wEbBBugzL/bP3VqXLi1Z5/B9u1/+POfRYEjg2Z8Q+L/K5AEiNS3rZw8MSRu7KtPlJ3/HCl84RDGtYbSLeaUwJj43y/YdOXhY/3ctS3zCDYGBw9/gke/gYPvP8ui+jn9koGcAAAAAElFTkSuQmCC",
  mauve:
    "iVBORw0KGgoAAAANSUhEUgAAABAAAAAMCAIAAADkharWAAAB4klEQVR42iWRaVPaUBiF70/yo35RahWxLmwqCIiQIAECJGSDJCxhURQBse6t4zbuM2qlrv3QUdtp/1YPdiaTuXPmOfe8573E3GV39Hj9/SFmmI2P8cnxVGKMj35K0OYoRE9f0N0bwD/wMQxRcirE3u2Z+UCDBpqyibxNhCEyHKcGIzB4TZTPRAcGwlBEh1zwFojPRIUtHVq0y5DgQQ5jYalBBgZ8wQEGtGCX8p78Mr1IwpYYNy4gS5nIyE4FBnaUgwgDLkYOaIhZd3aJqm7GVwlo2ZlWp7TMpIoDbxViI8m5oRhtjoTMUdC8VdRdejU4v862DqQdgos1l667s/BIDgV+lMOQCMGBswrQFwKVtWhzX9y+1A8JsjBcdjqXmVLRAWXA4WLkgIZY8ZdXI409YetCO2wXz4nhMwxvITedS0+qaIYVsSMcamASDFmaKa4w9V1+81w7AP28cEXK/lLRZyAHs6Vs0vs7dPYLP/TGXO0Lt3Gm7reLZz+q1z9rt6QyW0YIaqExDLgYOaAh1kNLO8m108zerdGhX5bbb427jgHPobk0GP4/BSaBUqMXtxKfj5Xdb4VT0K/177+bD7+a96TiL6E0VqFMpOFBS9BY+QbbOpK/3uRP3um7P63Hv60neP4BqKD/Yn8KB+AAAAAASUVORK5CYII=",
  moss:
    "iVBORw0KGgoAAAANSUhEUgAAABAAAAAMCAIAAADkharWAAAB30lEQVR42iWRW1PaUBRGz8/xtb6oddRWBQSBhFvkFoGQEAgCQUi436JIRMVRvLQ6OtrptB370Kl9amc6vY3/q9+RmfOQ7Flrf3ufQ6aYV9ORtfkUu5znrKXwmhaxlcIranAp60dxVnDNJJyzgntB9qDorMfIi7DtpURpWzniqPB2nadCYWNR8c1L7FzSPScyC2kvKuu1zcCuTPC/nOPQGP/OWgwOcuAvZnxIwJnQjirv30ltHufJ61wANLLczYSrHodg2Q6hCAEockDbK7y3J0WPcqm3OgHtasTZtsC0BXzY9ehqMQhhSfFjDUrrUU9XjBxsSZda4V2LoLGnm/R2RTjIgb+qQuDgrKiUZjvJ8DArXpTz90398y5BFobzGRIErIHVIaAxcijdFoKmIpyVtu4a2sNO89s+4fpyoC9DYFoJR5UKlmLIuh0CzbSEjUEmMS5mb+vagwHa+D4iQTPD9dPenojZsDEEHGwJn9tLx09U5aZW/mQ0Hym99/OEhPYVhGAtbPz8DlHkULovx44Lmetq6WOv8WgaP0aDX6fmnzEV8ByeThLC5CkwCSr8KC9fVYofOo2vlDZ/j4f/zod/zzGSgqXZ5zud3C9oeuVvNPV9u/5lgEnQ+PDp4vDpEs5/1YLsqJnpVR0AAAAASUVORK5CYII=",
  amber:
    "iVBORw0KGgoAAAANSUhEUgAAABAAAAAMCAIAAADkharWAAACAElEQVR42iWRXVMScRjF/9/Eq7oQ8YVAXkRcqDAVLRMBFQcUWDFEFsQFQQNEEfEFREVRc8SscYRKTQancSy9cay7vGiqsfwoHnRmZ2fnzO885znPkg55Cd380KXjjhp54xbBRF813q97H3m7KyEOqkvtbaUONcfdUQ4xNiAmFtUDp7YMdJiujvQLI1YhDGM9PI++wqXlOto5jKZsuLMcStQmTrEUYTQcv4GHwVGbaNomhidkEcDPdlUM6bgIYe/oqVeipeG6TEhJ/IYq0MiaHZTEBiQwBM18iDBgMHJAQ0wOybYCTz/MqAjoGbtknpHOMzUxu2TSKgyY+D5DlVdfiRqgoSy4ajfHnmSnm46XXhIMTrhqIcUZKXLgRzkkwIMP0HGndGP08V60sbDYev5GR5CF5ZJuGUJQC9XBYXDAVKQhrvkUu5GGfLL1bEP7452epDwUnkW3bM5Rg2bjtCBo4ofMfNBQ0iPy95PPjhIvvt3RP7NGkvYpVjwUcrDbVL8ICWFagJagV7zynXD9Yfz51zXN9x39Vdb465OJrPsVSEAHVIcBg5EDGuJ2SLk/13Kabi/SuZ7fB+a/ny1FwzJLJZxSGO5/RZFmqUxQ+XG2+WRVffm2C/SfQ/N1nr4+ogk6oTROBMP9fZfZOpw8F1N9SbVdZDqxCQb/L/TdFKz/8vQtKXP08tookJoAAAAASUVORK5CYII=",
};

// Which gradient stands in for which fixture. Two libraries hold images -- the
// data-engineer persona's and the signed-in reader's own -- so the gradients
// are named rather than keyed by resource id and are shared between them.
const IMAGE_BY_RESOURCE: Record<string, string> = {
  "res-029": "slate",
  "res-030": "mauve",
  "res-031": "moss",
  "res-032": "amber",
  "res-033": "moss",
  "res-034": "slate",
};

/** The image bytes for a resource, or undefined when the fixture is not one. */
export function resourceImageBytes(id: string): Uint8Array | undefined {
  const key = IMAGE_BY_RESOURCE[id];
  const encoded = key ? PNG[key] : undefined;
  if (!encoded) return undefined;
  const binary = atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}
