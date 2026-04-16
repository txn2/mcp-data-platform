import sharp from "sharp";
import fs from "fs";
import path from "path";
import { glob } from "fs/promises";

const outputDir =
  process.env["SCREENSHOT_OUTPUT_DIR"] ||
  path.resolve(process.cwd(), "..", "docs", "images", "screenshots");

async function convertPngsToWebp() {
  const themes = ["light", "dark"];
  let converted = 0;

  for (const theme of themes) {
    const themeDir = path.join(outputDir, theme);
    if (!fs.existsSync(themeDir)) continue;

    for await (const file of glob("*.png", { cwd: themeDir })) {
      const pngPath = path.join(themeDir, file);
      const webpPath = pngPath.replace(/\.png$/, ".webp");
      await sharp(pngPath).webp({ quality: 85 }).toFile(webpPath);
      fs.unlinkSync(pngPath);
      converted++;
    }
  }

  console.log(`Converted ${converted} PNG files to WebP`);
}

convertPngsToWebp().catch((err) => {
  console.error("Conversion failed:", err);
  process.exit(1);
});
