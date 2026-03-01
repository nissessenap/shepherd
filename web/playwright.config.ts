import { defineConfig } from "@playwright/test";

export default defineConfig({
	testDir: "./e2e",
	use: {
		baseURL: process.env.BASE_URL ?? "http://localhost:30081",
		colorScheme: "dark",
		trace: "on-first-retry",
		screenshot: "only-on-failure",
	},
	projects: [{ name: "chromium", use: { browserName: "chromium" } }],
	retries: 1,
	timeout: 60_000,
	reporter: [["html", { open: "never" }]],
});
