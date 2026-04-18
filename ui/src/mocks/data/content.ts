import { jsxDashboardContent } from "./jsx-dashboard-content";

/**
 * Mock content for each asset, keyed by asset ID.
 * These are returned by GET /assets/:id/content.
 */
export const mockContent: Record<string, string> = {
  "ast-001": `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>
  :root {
    --bg-primary: #f0f2f5;
    --bg-card: #ffffff;
    --bg-header: linear-gradient(135deg, #0f172a 0%, #1e293b 50%, #334155 100%);
    --text-primary: #1e293b;
    --text-secondary: #64748b;
    --text-muted: #94a3b8;
    --border: #e2e8f0;
    --border-light: #f1f5f9;
    --accent-blue: #3b82f6;
    --accent-indigo: #6366f1;
    --accent-violet: #8b5cf6;
    --accent-emerald: #10b981;
    --accent-amber: #f59e0b;
    --accent-rose: #f43f5e;
    --shadow-sm: 0 1px 2px rgba(0,0,0,0.04), 0 1px 4px rgba(0,0,0,0.06);
    --shadow-md: 0 2px 8px rgba(0,0,0,0.06), 0 4px 16px rgba(0,0,0,0.04);
    --shadow-lg: 0 4px 12px rgba(0,0,0,0.08), 0 8px 32px rgba(0,0,0,0.06);
    --radius: 14px;
    --radius-sm: 8px;
  }

  * { margin: 0; padding: 0; box-sizing: border-box; }

  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Inter, Roboto, sans-serif;
    background: var(--bg-primary);
    color: var(--text-primary);
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
  }

  .dashboard-header {
    background: var(--bg-header);
    padding: 32px 40px 28px;
    position: relative;
    overflow: hidden;
  }

  .dashboard-header::before {
    content: '';
    position: absolute;
    top: -50%;
    right: -10%;
    width: 400px;
    height: 400px;
    background: radial-gradient(circle, rgba(99,102,241,0.15) 0%, transparent 70%);
    border-radius: 50%;
  }

  .dashboard-header::after {
    content: '';
    position: absolute;
    bottom: -30%;
    left: 20%;
    width: 300px;
    height: 300px;
    background: radial-gradient(circle, rgba(59,130,246,0.1) 0%, transparent 70%);
    border-radius: 50%;
  }

  .header-content {
    position: relative;
    z-index: 1;
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
  }

  .header-left h1 {
    font-size: 1.625rem;
    font-weight: 700;
    color: #ffffff;
    letter-spacing: -0.02em;
  }

  .header-left p {
    color: rgba(255,255,255,0.55);
    font-size: 0.8125rem;
    margin-top: 4px;
    font-weight: 400;
  }

  .header-badge {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: rgba(16,185,129,0.15);
    border: 1px solid rgba(16,185,129,0.25);
    color: #6ee7b7;
    font-size: 0.6875rem;
    font-weight: 600;
    padding: 5px 12px;
    border-radius: 20px;
    letter-spacing: 0.02em;
    text-transform: uppercase;
  }

  .header-badge::before {
    content: '';
    width: 6px;
    height: 6px;
    background: #34d399;
    border-radius: 50%;
    animation: pulse-dot 2s ease-in-out infinite;
  }

  @keyframes pulse-dot {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
  }

  .dashboard-body {
    padding: 28px 40px 40px;
    max-width: 1400px;
    margin: 0 auto;
  }

  .kpi-grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 18px;
    margin-bottom: 28px;
  }

  .kpi-card {
    background: var(--bg-card);
    border-radius: var(--radius);
    padding: 22px 24px;
    box-shadow: var(--shadow-sm);
    border: 1px solid var(--border-light);
    position: relative;
    overflow: hidden;
    transition: box-shadow 0.2s, transform 0.2s;
  }

  .kpi-card:hover {
    box-shadow: var(--shadow-md);
    transform: translateY(-1px);
  }

  .kpi-card::after {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 3px;
  }

  .kpi-card:nth-child(1)::after { background: linear-gradient(90deg, #3b82f6, #6366f1); }
  .kpi-card:nth-child(2)::after { background: linear-gradient(90deg, #8b5cf6, #a855f7); }
  .kpi-card:nth-child(3)::after { background: linear-gradient(90deg, #10b981, #34d399); }
  .kpi-card:nth-child(4)::after { background: linear-gradient(90deg, #f59e0b, #fbbf24); }

  .kpi-top {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 12px;
  }

  .kpi-label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .kpi-trend {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    font-size: 0.6875rem;
    font-weight: 700;
    padding: 3px 8px;
    border-radius: 6px;
  }

  .kpi-trend.up {
    color: #059669;
    background: rgba(16,185,129,0.1);
  }

  .kpi-trend.down {
    color: #dc2626;
    background: rgba(220,38,38,0.08);
  }

  .kpi-value {
    font-size: 2rem;
    font-weight: 800;
    letter-spacing: -0.03em;
    color: var(--text-primary);
    line-height: 1.1;
  }

  .kpi-sparkline {
    margin-top: 14px;
    height: 32px;
  }

  .kpi-sparkline svg {
    width: 100%;
    height: 32px;
  }

  .section-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 18px;
    margin-bottom: 28px;
  }

  .panel {
    background: var(--bg-card);
    border-radius: var(--radius);
    box-shadow: var(--shadow-sm);
    border: 1px solid var(--border-light);
    overflow: hidden;
  }

  .panel-header {
    padding: 18px 24px 14px;
    border-bottom: 1px solid var(--border);
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .panel-title {
    font-size: 0.9375rem;
    font-weight: 700;
    color: var(--text-primary);
  }

  .panel-subtitle {
    font-size: 0.6875rem;
    color: var(--text-muted);
    font-weight: 500;
  }

  .panel-body {
    padding: 20px 24px;
  }

  .bar-row {
    display: flex;
    align-items: center;
    margin-bottom: 14px;
    gap: 12px;
  }

  .bar-row:last-child { margin-bottom: 0; }

  .bar-label {
    font-size: 0.8125rem;
    font-weight: 500;
    color: var(--text-secondary);
    width: 80px;
    flex-shrink: 0;
    text-align: right;
  }

  .bar-track {
    flex: 1;
    height: 26px;
    background: var(--border-light);
    border-radius: 6px;
    overflow: hidden;
    position: relative;
  }

  .bar-fill {
    height: 100%;
    border-radius: 6px;
    display: flex;
    align-items: center;
    padding-left: 10px;
    font-size: 0.6875rem;
    font-weight: 700;
    color: white;
    transition: width 0.6s cubic-bezier(0.22, 1, 0.36, 1);
    min-width: fit-content;
  }

  .bar-value {
    font-size: 0.8125rem;
    font-weight: 700;
    color: var(--text-primary);
    width: 72px;
    flex-shrink: 0;
    text-align: right;
  }

  .product-table {
    width: 100%;
    border-collapse: collapse;
  }

  .product-table thead th {
    text-align: left;
    padding: 10px 16px;
    font-size: 0.6875rem;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    background: var(--border-light);
    border-bottom: 1px solid var(--border);
  }

  .product-table thead th:last-child { text-align: center; }
  .product-table thead th:nth-child(4),
  .product-table thead th:nth-child(5) { text-align: right; }

  .product-table tbody td {
    padding: 10px 16px;
    font-size: 0.8125rem;
    border-bottom: 1px solid var(--border-light);
    color: var(--text-primary);
  }

  .product-table tbody td:nth-child(4),
  .product-table tbody td:nth-child(5) { text-align: right; font-variant-numeric: tabular-nums; }
  .product-table tbody td:last-child { text-align: center; }

  .product-table tbody tr:last-child td { border-bottom: none; }

  .product-table tbody tr:hover { background: rgba(241,245,249,0.5); }

  .product-name {
    font-weight: 600;
    color: var(--text-primary);
  }

  .product-cat {
    font-size: 0.6875rem;
    color: var(--text-muted);
    font-weight: 500;
  }

  .status-pill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 10px;
    border-radius: 12px;
    font-size: 0.6875rem;
    font-weight: 600;
    white-space: nowrap;
  }

  .status-pill.trending-up {
    background: rgba(16,185,129,0.1);
    color: #059669;
  }

  .status-pill.stable {
    background: rgba(59,130,246,0.1);
    color: #2563eb;
  }

  .status-pill.trending-down {
    background: rgba(244,63,94,0.08);
    color: #e11d48;
  }

  .trend-chart-section {
    background: var(--bg-card);
    border-radius: var(--radius);
    box-shadow: var(--shadow-sm);
    border: 1px solid var(--border-light);
    overflow: hidden;
  }

  .trend-chart-section .panel-header {
    border-bottom: 1px solid var(--border);
  }

  .trend-chart-body {
    padding: 24px 24px 20px;
  }

  .trend-chart-body svg {
    width: 100%;
    height: auto;
    display: block;
  }

  .footer-bar {
    margin-top: 24px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0 4px;
  }

  .footer-bar span {
    font-size: 0.6875rem;
    color: var(--text-muted);
  }
</style>
</head>
<body>
  <div class="dashboard-header">
    <div class="header-content">
      <div class="header-left">
        <h1>Q4 2025 Revenue Dashboard</h1>
        <p>Generated from warehouse data &middot; ${new Date().toLocaleDateString()}</p>
      </div>
      <div class="header-badge">Live Data</div>
    </div>
  </div>

  <div class="dashboard-body">
    <div class="kpi-grid">
      <div class="kpi-card">
        <div class="kpi-top">
          <div class="kpi-label">Total Revenue</div>
          <div class="kpi-trend up">&#9650; 12.3%</div>
        </div>
        <div class="kpi-value">$4.2M</div>
        <div class="kpi-sparkline">
          <svg viewBox="0 0 120 32" preserveAspectRatio="none">
            <defs>
              <linearGradient id="spark1" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stop-color="#3b82f6" stop-opacity="0.25"/>
                <stop offset="100%" stop-color="#3b82f6" stop-opacity="0"/>
              </linearGradient>
            </defs>
            <path d="M0,28 L10,24 L20,26 L30,22 L40,20 L50,18 L60,19 L70,14 L80,12 L90,10 L100,8 L110,5 L120,3 L120,32 L0,32Z" fill="url(#spark1)"/>
            <polyline points="0,28 10,24 20,26 30,22 40,20 50,18 60,19 70,14 80,12 90,10 100,8 110,5 120,3" fill="none" stroke="#3b82f6" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
      </div>

      <div class="kpi-card">
        <div class="kpi-top">
          <div class="kpi-label">Avg Order Value</div>
          <div class="kpi-trend up">&#9650; 5.1%</div>
        </div>
        <div class="kpi-value">$847</div>
        <div class="kpi-sparkline">
          <svg viewBox="0 0 120 32" preserveAspectRatio="none">
            <defs>
              <linearGradient id="spark2" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stop-color="#8b5cf6" stop-opacity="0.25"/>
                <stop offset="100%" stop-color="#8b5cf6" stop-opacity="0"/>
              </linearGradient>
            </defs>
            <path d="M0,22 L10,24 L20,20 L30,21 L40,18 L50,19 L60,16 L70,17 L80,13 L90,14 L100,10 L110,8 L120,6 L120,32 L0,32Z" fill="url(#spark2)"/>
            <polyline points="0,22 10,24 20,20 30,21 40,18 50,19 60,16 70,17 80,13 90,14 100,10 110,8 120,6" fill="none" stroke="#8b5cf6" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
      </div>

      <div class="kpi-card">
        <div class="kpi-top">
          <div class="kpi-label">Total Orders</div>
          <div class="kpi-trend up">&#9650; 8.7%</div>
        </div>
        <div class="kpi-value">4,958</div>
        <div class="kpi-sparkline">
          <svg viewBox="0 0 120 32" preserveAspectRatio="none">
            <defs>
              <linearGradient id="spark3" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stop-color="#10b981" stop-opacity="0.25"/>
                <stop offset="100%" stop-color="#10b981" stop-opacity="0"/>
              </linearGradient>
            </defs>
            <path d="M0,26 L10,22 L20,24 L30,20 L40,22 L50,16 L60,18 L70,14 L80,16 L90,10 L100,12 L110,7 L120,4 L120,32 L0,32Z" fill="url(#spark3)"/>
            <polyline points="0,26 10,22 20,24 30,20 40,22 50,16 60,18 70,14 80,16 90,10 100,12 110,7 120,4" fill="none" stroke="#10b981" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
      </div>

      <div class="kpi-card">
        <div class="kpi-top">
          <div class="kpi-label">Return Rate</div>
          <div class="kpi-trend down">&#9650; 0.4%</div>
        </div>
        <div class="kpi-value">3.2%</div>
        <div class="kpi-sparkline">
          <svg viewBox="0 0 120 32" preserveAspectRatio="none">
            <defs>
              <linearGradient id="spark4" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stop-color="#f59e0b" stop-opacity="0.25"/>
                <stop offset="100%" stop-color="#f59e0b" stop-opacity="0"/>
              </linearGradient>
            </defs>
            <path d="M0,18 L10,16 L20,19 L30,17 L40,20 L50,18 L60,21 L70,19 L80,22 L90,20 L100,22 L110,21 L120,23 L120,32 L0,32Z" fill="url(#spark4)"/>
            <polyline points="0,18 10,16 20,19 30,17 40,20 50,18 60,21 70,19 80,22 90,20 100,22 110,21 120,23" fill="none" stroke="#f59e0b" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
      </div>
    </div>

    <div class="section-grid">
      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">Revenue by Region</div>
            <div class="panel-subtitle">Q4 2025 breakdown</div>
          </div>
        </div>
        <div class="panel-body">
          <div class="bar-row">
            <div class="bar-label">West</div>
            <div class="bar-track"><div class="bar-fill" style="width:92%;background:linear-gradient(90deg,#3b82f6,#6366f1)">$1.54M</div></div>
            <div class="bar-value">$1.54M</div>
          </div>
          <div class="bar-row">
            <div class="bar-label">East</div>
            <div class="bar-track"><div class="bar-fill" style="width:75%;background:linear-gradient(90deg,#6366f1,#8b5cf6)">$1.26M</div></div>
            <div class="bar-value">$1.26M</div>
          </div>
          <div class="bar-row">
            <div class="bar-label">Central</div>
            <div class="bar-track"><div class="bar-fill" style="width:53%;background:linear-gradient(90deg,#8b5cf6,#a855f7)">$890K</div></div>
            <div class="bar-value">$890K</div>
          </div>
          <div class="bar-row">
            <div class="bar-label">South</div>
            <div class="bar-track"><div class="bar-fill" style="width:38%;background:linear-gradient(90deg,#a855f7,#c084fc)">$640K</div></div>
            <div class="bar-value">$640K</div>
          </div>
          <div class="bar-row">
            <div class="bar-label">Northwest</div>
            <div class="bar-track"><div class="bar-fill" style="width:30%;background:linear-gradient(90deg,#10b981,#34d399)">$510K</div></div>
            <div class="bar-value">$510K</div>
          </div>
          <div class="bar-row">
            <div class="bar-label">Southeast</div>
            <div class="bar-track"><div class="bar-fill" style="width:26%;background:linear-gradient(90deg,#14b8a6,#2dd4bf)">$438K</div></div>
            <div class="bar-value">$438K</div>
          </div>
          <div class="bar-row">
            <div class="bar-label">Midwest</div>
            <div class="bar-track"><div class="bar-fill" style="width:22%;background:linear-gradient(90deg,#f59e0b,#fbbf24)">$372K</div></div>
            <div class="bar-value">$372K</div>
          </div>
          <div class="bar-row">
            <div class="bar-label">Southwest</div>
            <div class="bar-track"><div class="bar-fill" style="width:17%;background:linear-gradient(90deg,#f97316,#fb923c)">$286K</div></div>
            <div class="bar-value">$286K</div>
          </div>
        </div>
      </div>

      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">Top Products by Revenue</div>
            <div class="panel-subtitle">Ranked by Q4 performance</div>
          </div>
        </div>
        <table class="product-table">
          <thead>
            <tr>
              <th>Product</th>
              <th>Category</th>
              <th style="text-align:right">Units</th>
              <th>Revenue</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td><span class="product-name">Enterprise Suite Pro</span></td>
              <td><span class="product-cat">Software</span></td>
              <td style="text-align:right">1,240</td>
              <td>$892,400</td>
              <td><span class="status-pill trending-up">&#9650; Trending</span></td>
            </tr>
            <tr>
              <td><span class="product-name">CloudSync Platform</span></td>
              <td><span class="product-cat">Infrastructure</span></td>
              <td style="text-align:right">890</td>
              <td>$712,000</td>
              <td><span class="status-pill trending-up">&#9650; Trending</span></td>
            </tr>
            <tr>
              <td><span class="product-name">DataVault Storage</span></td>
              <td><span class="product-cat">Storage</span></td>
              <td style="text-align:right">2,100</td>
              <td>$588,000</td>
              <td><span class="status-pill stable">&#9679; Stable</span></td>
            </tr>
            <tr>
              <td><span class="product-name">Analytics Core</span></td>
              <td><span class="product-cat">Analytics</span></td>
              <td style="text-align:right">680</td>
              <td>$476,000</td>
              <td><span class="status-pill trending-up">&#9650; Trending</span></td>
            </tr>
            <tr>
              <td><span class="product-name">SecureNet Gateway</span></td>
              <td><span class="product-cat">Security</span></td>
              <td style="text-align:right">520</td>
              <td>$364,000</td>
              <td><span class="status-pill stable">&#9679; Stable</span></td>
            </tr>
            <tr>
              <td><span class="product-name">API Manager Plus</span></td>
              <td><span class="product-cat">Integration</span></td>
              <td style="text-align:right">940</td>
              <td>$282,000</td>
              <td><span class="status-pill trending-down">&#9660; Declining</span></td>
            </tr>
            <tr>
              <td><span class="product-name">EdgeCompute Nodes</span></td>
              <td><span class="product-cat">Infrastructure</span></td>
              <td style="text-align:right">310</td>
              <td>$248,000</td>
              <td><span class="status-pill trending-up">&#9650; Trending</span></td>
            </tr>
            <tr>
              <td><span class="product-name">DevOps Toolkit</span></td>
              <td><span class="product-cat">Developer Tools</span></td>
              <td style="text-align:right">1,580</td>
              <td>$205,400</td>
              <td><span class="status-pill stable">&#9679; Stable</span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="trend-chart-section">
      <div class="panel-header">
        <div>
          <div class="panel-title">Monthly Revenue Trend</div>
          <div class="panel-subtitle">Jan 2025 &ndash; Dec 2025</div>
        </div>
        <div class="panel-subtitle">Total: $42.8M</div>
      </div>
      <div class="trend-chart-body">
        <svg viewBox="0 0 900 240" preserveAspectRatio="xMidYMid meet">
          <defs>
            <linearGradient id="areaGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stop-color="#6366f1" stop-opacity="0.3"/>
              <stop offset="60%" stop-color="#3b82f6" stop-opacity="0.08"/>
              <stop offset="100%" stop-color="#3b82f6" stop-opacity="0"/>
            </linearGradient>
            <linearGradient id="lineGrad" x1="0" y1="0" x2="1" y2="0">
              <stop offset="0%" stop-color="#3b82f6"/>
              <stop offset="50%" stop-color="#6366f1"/>
              <stop offset="100%" stop-color="#8b5cf6"/>
            </linearGradient>
            <filter id="glow">
              <feGaussianBlur stdDeviation="2" result="blur"/>
              <feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge>
            </filter>
          </defs>

          <line x1="60" y1="200" x2="860" y2="200" stroke="#e2e8f0" stroke-width="1"/>
          <line x1="60" y1="155" x2="860" y2="155" stroke="#f1f5f9" stroke-width="1" stroke-dasharray="4,4"/>
          <line x1="60" y1="110" x2="860" y2="110" stroke="#f1f5f9" stroke-width="1" stroke-dasharray="4,4"/>
          <line x1="60" y1="65" x2="860" y2="65" stroke="#f1f5f9" stroke-width="1" stroke-dasharray="4,4"/>
          <line x1="60" y1="20" x2="860" y2="20" stroke="#f1f5f9" stroke-width="1" stroke-dasharray="4,4"/>

          <text x="50" y="203" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="end">$0</text>
          <text x="50" y="158" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="end">$1M</text>
          <text x="50" y="113" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="end">$2M</text>
          <text x="50" y="68" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="end">$3M</text>
          <text x="50" y="23" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="end">$4M</text>

          <path d="M93,168 L160,160 L226,152 L293,140 L360,134 L426,118 L493,110 L560,98 L626,88 L693,72 L760,60 L826,42 L826,200 L93,200Z" fill="url(#areaGrad)"/>

          <polyline points="93,168 160,160 226,152 293,140 360,134 426,118 493,110 560,98 626,88 693,72 760,60 826,42" fill="none" stroke="url(#lineGrad)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" filter="url(#glow)"/>

          <circle cx="93" cy="168" r="3.5" fill="#ffffff" stroke="#3b82f6" stroke-width="2"/>
          <circle cx="160" cy="160" r="3.5" fill="#ffffff" stroke="#4f6af1" stroke-width="2"/>
          <circle cx="226" cy="152" r="3.5" fill="#ffffff" stroke="#5564f0" stroke-width="2"/>
          <circle cx="293" cy="140" r="3.5" fill="#ffffff" stroke="#5b5eef" stroke-width="2"/>
          <circle cx="360" cy="134" r="3.5" fill="#ffffff" stroke="#6366f1" stroke-width="2"/>
          <circle cx="426" cy="118" r="3.5" fill="#ffffff" stroke="#6a63f0" stroke-width="2"/>
          <circle cx="493" cy="110" r="3.5" fill="#ffffff" stroke="#7060ef" stroke-width="2"/>
          <circle cx="560" cy="98" r="3.5" fill="#ffffff" stroke="#775ded" stroke-width="2"/>
          <circle cx="626" cy="88" r="3.5" fill="#ffffff" stroke="#7e5aec" stroke-width="2"/>
          <circle cx="693" cy="72" r="3.5" fill="#ffffff" stroke="#8458eb" stroke-width="2"/>
          <circle cx="760" cy="60" r="3.5" fill="#ffffff" stroke="#8856ea" stroke-width="2"/>
          <circle cx="826" cy="42" r="4" fill="#8b5cf6" stroke="#ffffff" stroke-width="2"/>

          <text x="93" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Jan</text>
          <text x="160" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Feb</text>
          <text x="226" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Mar</text>
          <text x="293" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Apr</text>
          <text x="360" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">May</text>
          <text x="426" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Jun</text>
          <text x="493" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Jul</text>
          <text x="560" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Aug</text>
          <text x="626" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Sep</text>
          <text x="693" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Oct</text>
          <text x="760" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Nov</text>
          <text x="826" y="218" font-family="-apple-system,system-ui,sans-serif" font-size="9" fill="#94a3b8" text-anchor="middle">Dec</text>

          <text x="826" y="35" font-family="-apple-system,system-ui,sans-serif" font-size="9" font-weight="700" fill="#6366f1" text-anchor="middle">$4.2M</text>
          <text x="93" y="163" font-family="-apple-system,system-ui,sans-serif" font-size="9" font-weight="600" fill="#94a3b8" text-anchor="middle">$2.8M</text>
        </svg>
      </div>
    </div>

    <div class="footer-bar">
      <span>MCP Data Platform &middot; Auto-generated report</span>
      <span>Last refreshed: ${new Date().toLocaleDateString()} at ${new Date().toLocaleTimeString()}</span>
    </div>
  </div>
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

  "ast-004": `export default function StorePerformance() {
  const metrics = [
    { label: "Revenue", value: "$1.54M", change: "+15.2%", positive: true, color: "#6366f1", icon: "\u0024" },
    { label: "Transactions", value: "12,847", change: "+8.3%", positive: true, color: "#3b82f6", icon: "\u2261" },
    { label: "Avg Basket", value: "$119.80", change: "-2.1%", positive: false, color: "#f59e0b", icon: "\u25CE" },
    { label: "Customer Footfall", value: "45,200", change: "+11.7%", positive: true, color: "#10b981", icon: "\u2302" },
    { label: "Conversion Rate", value: "28.4%", change: "+3.2%", positive: true, color: "#8b5cf6", icon: "\u25C9" },
    { label: "Return Rate", value: "2.8%", change: "-0.5%", positive: true, color: "#06b6d4", icon: "\u21A9" },
  ];

  const categories = [
    { name: "Electronics", pct: 34, from: "#6366f1", to: "#818cf8" },
    { name: "Home & Garden", pct: 22, from: "#10b981", to: "#34d399" },
    { name: "Sporting Goods", pct: 18, from: "#f59e0b", to: "#fbbf24" },
    { name: "Seasonal", pct: 15, from: "#ef4444", to: "#f87171" },
    { name: "Grocery", pct: 11, from: "#3b82f6", to: "#60a5fa" },
  ];

  const cardStyle = (color) => ({
    background: "#ffffff",
    borderRadius: "14px",
    padding: "24px",
    boxShadow: "0 1px 3px rgba(0,0,0,0.06), 0 6px 16px rgba(0,0,0,0.04)",
    borderTop: "3px solid " + color,
    position: "relative",
    overflow: "hidden",
  });

  const shimmer = (color) => ({
    position: "absolute",
    top: 0,
    right: 0,
    width: "80px",
    height: "80px",
    borderRadius: "50%",
    background: color,
    opacity: 0.06,
    transform: "translate(30px, -30px)",
  });

  return (
    <div style={{ fontFamily: "'Inter', 'Segoe UI', system-ui, -apple-system, sans-serif", background: "linear-gradient(135deg, #f0f4ff 0%, #faf5ff 50%, #f0fdf4 100%)", minHeight: "100%", padding: "32px", color: "#1e293b" }}>
      <div style={{ maxWidth: "960px", margin: "0 auto" }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "28px", flexWrap: "wrap", gap: "12px" }}>
          <div>
            <div style={{ display: "flex", alignItems: "center", gap: "10px", marginBottom: "4px" }}>
              <div style={{ width: "10px", height: "10px", borderRadius: "50%", background: "#22c55e", boxShadow: "0 0 8px rgba(34,197,94,0.5)" }} />
              <span style={{ fontSize: "0.7rem", fontWeight: 600, color: "#16a34a", textTransform: "uppercase", letterSpacing: "0.1em" }}>Live</span>
            </div>
            <h1 style={{ margin: 0, fontSize: "1.6rem", fontWeight: 800, letterSpacing: "-0.02em", background: "linear-gradient(135deg, #312e81, #6366f1)", WebkitBackgroundClip: "text", WebkitTextFillColor: "transparent" }}>ACME Corp Store Performance</h1>
            <p style={{ margin: "4px 0 0", fontSize: "0.8rem", color: "#64748b" }}>Q1 2026 Consolidated Metrics</p>
          </div>
          <div style={{ position: "relative" }}>
            <select style={{ appearance: "none", background: "#ffffff", border: "1px solid #e2e8f0", borderRadius: "10px", padding: "10px 36px 10px 14px", fontSize: "0.8rem", fontWeight: 500, color: "#334155", cursor: "pointer", boxShadow: "0 1px 2px rgba(0,0,0,0.04)", outline: "none" }}>
              <option>All Stores (24)</option>
              <option>Downtown Flagship</option>
              <option>Westfield Mall</option>
              <option>Airport Terminal 3</option>
              <option>Online Store</option>
            </select>
            <span style={{ position: "absolute", right: "12px", top: "50%", transform: "translateY(-50%)", pointerEvents: "none", fontSize: "0.7rem", color: "#94a3b8" }}>\u25BC</span>
          </div>
        </div>

        <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: "16px", marginBottom: "28px" }}>
          {metrics.map((m) => (
            <div key={m.label} style={cardStyle(m.color)}>
              <div style={shimmer(m.color)} />
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
                <span style={{ fontSize: "0.68rem", fontWeight: 600, color: "#94a3b8", textTransform: "uppercase", letterSpacing: "0.08em" }}>{m.label}</span>
                <span style={{ fontSize: "1.1rem", opacity: 0.25 }}>{m.icon}</span>
              </div>
              <div style={{ fontSize: "2rem", fontWeight: 800, marginTop: "8px", letterSpacing: "-0.02em", color: "#0f172a" }}>{m.value}</div>
              <div style={{ display: "inline-flex", alignItems: "center", gap: "4px", marginTop: "10px", padding: "3px 10px", borderRadius: "20px", fontSize: "0.72rem", fontWeight: 600, background: m.positive ? "rgba(34,197,94,0.1)" : "rgba(239,68,68,0.1)", color: m.positive ? "#15803d" : "#dc2626" }}>
                <span style={{ fontSize: "0.8rem" }}>{m.positive ? "\u2191" : "\u2193"}</span>
                {m.change} vs last quarter
              </div>
            </div>
          ))}
        </div>

        <div style={{ background: "#ffffff", borderRadius: "14px", padding: "28px", boxShadow: "0 1px 3px rgba(0,0,0,0.06), 0 6px 16px rgba(0,0,0,0.04)" }}>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "20px" }}>
            <div>
              <h2 style={{ margin: 0, fontSize: "1rem", fontWeight: 700, color: "#0f172a" }}>Top Selling Categories</h2>
              <p style={{ margin: "2px 0 0", fontSize: "0.75rem", color: "#94a3b8" }}>Share of total revenue by department</p>
            </div>
            <span style={{ fontSize: "0.68rem", fontWeight: 500, color: "#94a3b8", textTransform: "uppercase", letterSpacing: "0.08em" }}>Last 90 days</span>
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
            {categories.map((cat) => (
              <div key={cat.name}>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", marginBottom: "6px" }}>
                  <span style={{ fontSize: "0.82rem", fontWeight: 600, color: "#334155" }}>{cat.name}</span>
                  <span style={{ fontSize: "0.82rem", fontWeight: 700, color: "#0f172a" }}>{cat.pct}%</span>
                </div>
                <div style={{ background: "#f1f5f9", borderRadius: "6px", height: "10px", overflow: "hidden" }}>
                  <div style={{ width: cat.pct + "%", height: "100%", borderRadius: "6px", background: "linear-gradient(90deg, " + cat.from + ", " + cat.to + ")", transition: "width 0.6s ease" }} />
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
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

  "ast-007": jsxDashboardContent,

  "ast-008": `Region,Quarter,Revenue,Units Sold,Avg Price,Growth %
West,Q1 2025,1540000,18200,84.62,15.2
East,Q1 2025,1260000,14880,84.68,11.8
Central,Q1 2025,890000,10500,84.76,9.4
South,Q1 2025,510000,6000,85.00,7.1
West,Q2 2025,1720000,19800,86.87,11.7
East,Q2 2025,1380000,15900,86.79,9.5
Central,Q2 2025,960000,11200,85.71,7.9
South,Q2 2025,580000,6700,86.57,13.7`,
};
