Scalar.createApiReference('#app', {
  url: '/api/openapi.yaml',
  withDefaultFonts: false,
  telemetry: false,
  agent: { disabled: true },
  mcp: { disabled: true },
  expandAllModelSections: true,
  expandAllResponses: true,
  expandAllSchemaProperties: true,
  customCss: 'a[href*="scalar.com"] { display: none !important; }',
});
