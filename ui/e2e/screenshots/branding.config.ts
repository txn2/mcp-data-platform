import fs from "fs";

export interface BrandingConfig {
  platformName?: string;
  portalTitle?: string;
  portalLogo?: string;
  implementorLogo?: string;
  outputDir?: string;
  prefix?: string;
}

export function loadBrandingConfig(): BrandingConfig {
  const configPath = process.env["SCREENSHOT_BRANDING_FILE"];

  let fileConfig: BrandingConfig = {};
  if (configPath && fs.existsSync(configPath)) {
    fileConfig = JSON.parse(fs.readFileSync(configPath, "utf-8"));
  }

  return {
    platformName:
      process.env["SCREENSHOT_PLATFORM_NAME"] ?? fileConfig.platformName,
    portalTitle:
      process.env["SCREENSHOT_PORTAL_TITLE"] ?? fileConfig.portalTitle,
    portalLogo:
      process.env["SCREENSHOT_PORTAL_LOGO"] ?? fileConfig.portalLogo,
    implementorLogo:
      process.env["SCREENSHOT_IMPLEMENTOR_LOGO"] ?? fileConfig.implementorLogo,
    outputDir:
      process.env["SCREENSHOT_OUTPUT_DIR"] ?? fileConfig.outputDir,
    prefix: process.env["SCREENSHOT_PREFIX"] ?? fileConfig.prefix ?? "",
  };
}
