/**
 * Mock content for each asset, keyed by asset ID.
 * These are returned by GET /assets/:id/content.
 */
export const mockContent: Record<string, string> = {
  "ast-001": `<!DOCTYPE html>
<html>
<head><style>
  body { font-family: system-ui, sans-serif; margin: 0; padding: 24px; background: #f8fafc; color: #1e293b; }
  .header { margin-bottom: 24px; }
  .header h1 { font-size: 1.5rem; margin: 0; }
  .header p { color: #64748b; margin: 4px 0 0; font-size: 0.875rem; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 24px; }
  .card { background: white; border-radius: 12px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  .card .label { font-size: 0.75rem; color: #94a3b8; text-transform: uppercase; letter-spacing: 0.05em; }
  .card .value { font-size: 1.75rem; font-weight: 700; margin-top: 4px; }
  .card .change { font-size: 0.75rem; margin-top: 4px; }
  .up { color: #16a34a; }
  .down { color: #dc2626; }
  table { width: 100%; border-collapse: collapse; background: white; border-radius: 12px; overflow: hidden; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  th { text-align: left; padding: 12px 16px; background: #f1f5f9; font-size: 0.75rem; color: #64748b; text-transform: uppercase; }
  td { padding: 12px 16px; border-top: 1px solid #e2e8f0; font-size: 0.875rem; }
</style></head>
<body>
  <div class="header">
    <h1>Q4 2025 Revenue Dashboard</h1>
    <p>Generated from warehouse data on ${new Date().toLocaleDateString()}</p>
  </div>
  <div class="grid">
    <div class="card">
      <div class="label">Total Revenue</div>
      <div class="value">$4.2M</div>
      <div class="change up">+12.3% vs Q3</div>
    </div>
    <div class="card">
      <div class="label">Avg Order Value</div>
      <div class="value">$847</div>
      <div class="change up">+5.1% vs Q3</div>
    </div>
    <div class="card">
      <div class="label">Total Orders</div>
      <div class="value">4,958</div>
      <div class="change up">+8.7% vs Q3</div>
    </div>
    <div class="card">
      <div class="label">Return Rate</div>
      <div class="value">3.2%</div>
      <div class="change down">+0.4% vs Q3</div>
    </div>
  </div>
  <table>
    <thead><tr><th>Region</th><th>Revenue</th><th>Orders</th><th>Growth</th></tr></thead>
    <tbody>
      <tr><td>West</td><td>$1,540,000</td><td>1,820</td><td class="up">+15.2%</td></tr>
      <tr><td>East</td><td>$1,260,000</td><td>1,488</td><td class="up">+11.8%</td></tr>
      <tr><td>Central</td><td>$890,000</td><td>1,050</td><td class="up">+9.4%</td></tr>
      <tr><td>South</td><td>$510,000</td><td>600</td><td class="up">+7.1%</td></tr>
    </tbody>
  </table>
</body>
</html>`,

  "ast-002": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 400">
  <defs>
    <linearGradient id="g1" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="#3b82f6" stop-opacity="0.8"/>
      <stop offset="100%" stop-color="#3b82f6" stop-opacity="0.2"/>
    </linearGradient>
  </defs>
  <rect width="600" height="400" fill="#f8fafc" rx="8"/>
  <text x="20" y="35" font-family="system-ui" font-size="18" font-weight="bold" fill="#1e293b">Sales Pipeline</text>
  <text x="20" y="55" font-family="system-ui" font-size="12" fill="#94a3b8">Current quarter stages</text>
  <!-- Funnel bars -->
  <rect x="80" y="80" width="440" height="45" rx="6" fill="#3b82f6" opacity="0.9"/>
  <text x="300" y="108" font-family="system-ui" font-size="14" fill="white" text-anchor="middle" font-weight="600">Leads: 1,240</text>
  <rect x="120" y="140" width="360" height="45" rx="6" fill="#6366f1" opacity="0.85"/>
  <text x="300" y="168" font-family="system-ui" font-size="14" fill="white" text-anchor="middle" font-weight="600">Qualified: 680</text>
  <rect x="160" y="200" width="280" height="45" rx="6" fill="#8b5cf6" opacity="0.8"/>
  <text x="300" y="228" font-family="system-ui" font-size="14" fill="white" text-anchor="middle" font-weight="600">Proposal: 310</text>
  <rect x="200" y="260" width="200" height="45" rx="6" fill="#a855f7" opacity="0.75"/>
  <text x="300" y="288" font-family="system-ui" font-size="14" fill="white" text-anchor="middle" font-weight="600">Negotiation: 145</text>
  <rect x="240" y="320" width="120" height="45" rx="6" fill="#16a34a" opacity="0.9"/>
  <text x="300" y="348" font-family="system-ui" font-size="14" fill="white" text-anchor="middle" font-weight="600">Won: 82</text>
</svg>`,

  "ast-003": `# Weekly Inventory Report

**Week of ${new Date().toLocaleDateString()}**

## Summary

| Metric | Value | Change |
|--------|-------|--------|
| Total SKUs | 12,450 | +120 |
| In Stock | 11,200 | +95 |
| Low Stock | 890 | -30 |
| Out of Stock | 360 | +55 |

## Warehouse Breakdown

### West Distribution Center
- **Capacity**: 85% utilized
- **Items shipped**: 4,200 this week
- **Restock needed**: Cleaning supplies, seasonal items

### East Distribution Center
- **Capacity**: 72% utilized
- **Items shipped**: 3,800 this week
- **Restock needed**: Electronics, batteries

### Central Warehouse
- **Capacity**: 91% utilized (near capacity)
- **Items shipped**: 5,100 this week
- **Action required**: Transfer overflow to East DC

## Alerts

> **High Priority**: 12 SKUs have been out of stock for 7+ days.
> See appendix for full list.

---

*Report auto-generated from warehouse management data via MCP Data Platform.*`,

  "ast-004": `function KPIScorecard() {
  const kpis = [
    { label: "Revenue", value: "$4.2M", trend: 12.3, target: "$4.0M", status: "above" },
    { label: "Customers", value: "2,847", trend: 8.1, target: "2,500", status: "above" },
    { label: "NPS Score", value: "72", trend: -2.0, target: "75", status: "below" },
    { label: "Churn Rate", value: "3.1%", trend: -0.5, target: "3.5%", status: "above" },
  ];

  return (
    <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))", gap: "16px" }}>
      {kpis.map((kpi) => (
        <div
          key={kpi.label}
          style={{
            background: "white",
            borderRadius: "12px",
            padding: "20px",
            boxShadow: "0 1px 3px rgba(0,0,0,0.08)",
            borderLeft: kpi.status === "above" ? "4px solid #16a34a" : "4px solid #eab308",
          }}
        >
          <div style={{ fontSize: "0.75rem", color: "#94a3b8", textTransform: "uppercase", letterSpacing: "0.05em" }}>
            {kpi.label}
          </div>
          <div style={{ fontSize: "1.75rem", fontWeight: 700, marginTop: "4px" }}>{kpi.value}</div>
          <div style={{ display: "flex", justifyContent: "space-between", marginTop: "8px", fontSize: "0.75rem" }}>
            <span style={{ color: kpi.trend >= 0 ? "#16a34a" : "#dc2626" }}>
              {kpi.trend >= 0 ? "\\u2191" : "\\u2193"} {Math.abs(kpi.trend)}%
            </span>
            <span style={{ color: "#94a3b8" }}>Target: {kpi.target}</span>
          </div>
        </div>
      ))}
    </div>
  );
}`,

  "ast-005": `<!DOCTYPE html>
<html>
<head><style>
  body { font-family: system-ui, sans-serif; margin: 0; padding: 24px; background: #f8fafc; color: #1e293b; }
  h1 { font-size: 1.5rem; margin: 0 0 4px; }
  .subtitle { color: #64748b; font-size: 0.875rem; margin-bottom: 24px; }
  .segments { display: grid; grid-template-columns: repeat(auto-fit, minmax(250px, 1fr)); gap: 16px; }
  .segment { background: white; border-radius: 12px; padding: 20px; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  .segment h3 { margin: 0 0 8px; font-size: 1rem; }
  .segment .count { font-size: 0.75rem; color: #64748b; }
  .bar-bg { background: #e2e8f0; border-radius: 4px; height: 8px; margin: 8px 0; }
  .bar-fill { height: 8px; border-radius: 4px; }
  .metrics { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-top: 12px; }
  .metric { font-size: 0.75rem; }
  .metric .val { font-weight: 600; font-size: 0.875rem; }
</style></head>
<body>
  <h1>Customer Segmentation Analysis</h1>
  <p class="subtitle">RFM-based segmentation across 28,500 active customers</p>
  <div class="segments">
    <div class="segment">
      <h3>Champions</h3>
      <span class="count">3,420 customers (12%)</span>
      <div class="bar-bg"><div class="bar-fill" style="width:12%;background:#16a34a"></div></div>
      <div class="metrics">
        <div class="metric"><div class="val">$2,400</div>Avg LTV</div>
        <div class="metric"><div class="val">4.2</div>Orders/mo</div>
      </div>
    </div>
    <div class="segment">
      <h3>Loyal Customers</h3>
      <span class="count">5,700 customers (20%)</span>
      <div class="bar-bg"><div class="bar-fill" style="width:20%;background:#3b82f6"></div></div>
      <div class="metrics">
        <div class="metric"><div class="val">$1,100</div>Avg LTV</div>
        <div class="metric"><div class="val">2.1</div>Orders/mo</div>
      </div>
    </div>
    <div class="segment">
      <h3>Potential Loyalists</h3>
      <span class="count">7,125 customers (25%)</span>
      <div class="bar-bg"><div class="bar-fill" style="width:25%;background:#8b5cf6"></div></div>
      <div class="metrics">
        <div class="metric"><div class="val">$580</div>Avg LTV</div>
        <div class="metric"><div class="val">1.3</div>Orders/mo</div>
      </div>
    </div>
    <div class="segment">
      <h3>At Risk</h3>
      <span class="count">4,275 customers (15%)</span>
      <div class="bar-bg"><div class="bar-fill" style="width:15%;background:#eab308"></div></div>
      <div class="metrics">
        <div class="metric"><div class="val">$420</div>Avg LTV</div>
        <div class="metric"><div class="val">0.4</div>Orders/mo</div>
      </div>
    </div>
    <div class="segment">
      <h3>Hibernating</h3>
      <span class="count">7,980 customers (28%)</span>
      <div class="bar-bg"><div class="bar-fill" style="width:28%;background:#dc2626"></div></div>
      <div class="metrics">
        <div class="metric"><div class="val">$190</div>Avg LTV</div>
        <div class="metric"><div class="val">0.1</div>Orders/mo</div>
      </div>
    </div>
  </div>
</body>
</html>`,

  "ast-006": `# Data Quality Summary

**Last updated**: ${new Date().toLocaleDateString()}

## Overall Health

| Score | Category | Details |
|-------|----------|---------|
| 94% | Completeness | 6% null values in optional fields |
| 99% | Uniqueness | 12 duplicate records found in staging |
| 97% | Timeliness | All pipelines within SLA |
| 88% | Accuracy | 3 tables flagged for review |

## Flagged Tables

### \`analytics.daily_sales\`
- **Issue**: Row count dropped 40% on Feb 28
- **Cause**: Upstream source delay (resolved)
- **Status**: Backfill complete

### \`warehouse.inventory_snapshots\`
- **Issue**: Negative stock values in 8 records
- **Cause**: Race condition in adjustment pipeline
- **Status**: Fix deployed, monitoring

### \`customers.profiles\`
- **Issue**: Email validation failures (2.1%)
- **Cause**: Legacy data migration
- **Status**: Cleanup scheduled

## Recommendations

1. Add data contract for \`daily_sales\` with row count bounds
2. Implement idempotent inventory adjustments
3. Run email normalization batch job

---

*Generated by MCP Data Platform quality monitoring.*`,

  // Shared assets content
  "ast-ext-001": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 350">
  <rect width="600" height="350" fill="#f8fafc" rx="8"/>
  <text x="20" y="35" font-family="system-ui" font-size="18" font-weight="bold" fill="#1e293b">Monthly Sales Trends</text>
  <text x="20" y="55" font-family="system-ui" font-size="12" fill="#94a3b8">Last 12 months</text>
  <!-- Axes -->
  <line x1="60" y1="280" x2="560" y2="280" stroke="#e2e8f0" stroke-width="1"/>
  <line x1="60" y1="80" x2="60" y2="280" stroke="#e2e8f0" stroke-width="1"/>
  <!-- Grid lines -->
  <line x1="60" y1="180" x2="560" y2="180" stroke="#e2e8f0" stroke-width="0.5" stroke-dasharray="4"/>
  <line x1="60" y1="130" x2="560" y2="130" stroke="#e2e8f0" stroke-width="0.5" stroke-dasharray="4"/>
  <line x1="60" y1="230" x2="560" y2="230" stroke="#e2e8f0" stroke-width="0.5" stroke-dasharray="4"/>
  <!-- Line chart -->
  <polyline
    points="100,240 140,235 180,220 220,210 260,195 300,185 340,178 380,165 420,158 460,140 500,130 540,120"
    fill="none" stroke="#3b82f6" stroke-width="2.5" stroke-linejoin="round"/>
  <!-- Data points -->
  <circle cx="100" cy="240" r="4" fill="#3b82f6"/><circle cx="140" cy="235" r="4" fill="#3b82f6"/>
  <circle cx="180" cy="220" r="4" fill="#3b82f6"/><circle cx="220" cy="210" r="4" fill="#3b82f6"/>
  <circle cx="260" cy="195" r="4" fill="#3b82f6"/><circle cx="300" cy="185" r="4" fill="#3b82f6"/>
  <circle cx="340" cy="178" r="4" fill="#3b82f6"/><circle cx="380" cy="165" r="4" fill="#3b82f6"/>
  <circle cx="420" cy="158" r="4" fill="#3b82f6"/><circle cx="460" cy="140" r="4" fill="#3b82f6"/>
  <circle cx="500" cy="130" r="4" fill="#3b82f6"/><circle cx="540" cy="120" r="4" fill="#3b82f6"/>
  <!-- Month labels -->
  <text x="100" y="298" font-family="system-ui" font-size="10" fill="#94a3b8" text-anchor="middle">Mar</text>
  <text x="220" y="298" font-family="system-ui" font-size="10" fill="#94a3b8" text-anchor="middle">Jun</text>
  <text x="340" y="298" font-family="system-ui" font-size="10" fill="#94a3b8" text-anchor="middle">Sep</text>
  <text x="460" y="298" font-family="system-ui" font-size="10" fill="#94a3b8" text-anchor="middle">Dec</text>
  <text x="540" y="298" font-family="system-ui" font-size="10" fill="#94a3b8" text-anchor="middle">Feb</text>
</svg>`,

  "ast-ext-002": `<!DOCTYPE html>
<html>
<head><style>
  body { font-family: system-ui, sans-serif; margin: 0; padding: 24px; background: #f8fafc; color: #1e293b; }
  h1 { font-size: 1.5rem; margin: 0 0 4px; }
  .subtitle { color: #64748b; font-size: 0.875rem; margin-bottom: 24px; }
  table { width: 100%; border-collapse: collapse; background: white; border-radius: 12px; overflow: hidden; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  th { text-align: left; padding: 12px 16px; background: #f1f5f9; font-size: 0.75rem; color: #64748b; text-transform: uppercase; }
  td { padding: 12px 16px; border-top: 1px solid #e2e8f0; font-size: 0.875rem; }
  .good { color: #16a34a; font-weight: 600; }
  .warn { color: #eab308; font-weight: 600; }
  .bad { color: #dc2626; font-weight: 600; }
</style></head>
<body>
  <h1>API Latency Report</h1>
  <p class="subtitle">P50, P95, and P99 response times by endpoint</p>
  <table>
    <thead><tr><th>Endpoint</th><th>P50</th><th>P95</th><th>P99</th><th>Status</th></tr></thead>
    <tbody>
      <tr><td>GET /api/products</td><td>45ms</td><td>120ms</td><td>250ms</td><td class="good">Healthy</td></tr>
      <tr><td>GET /api/orders</td><td>82ms</td><td>340ms</td><td>890ms</td><td class="warn">Elevated</td></tr>
      <tr><td>POST /api/checkout</td><td>210ms</td><td>850ms</td><td>2400ms</td><td class="bad">Degraded</td></tr>
      <tr><td>GET /api/inventory</td><td>38ms</td><td>95ms</td><td>180ms</td><td class="good">Healthy</td></tr>
      <tr><td>GET /api/users</td><td>55ms</td><td>150ms</td><td>320ms</td><td class="good">Healthy</td></tr>
    </tbody>
  </table>
</body>
</html>`,
};
