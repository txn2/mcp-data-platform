// Production artifact mock: 25KB JSX dashboard with recharts, useState, tabs.
export const jsxDashboardContent = `import { useState } from "react";
import { BarChart, Bar, LineChart, Line, PieChart, Pie, Cell, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Area, AreaChart, Legend } from "recharts";

const BRAND = {
  primary: "#1a365d",
  secondary: "#e53e3e",
  accent: "#2b6cb0",
  link: "#3182ce",
  text: "#2d3748",
  textLight: "#718096",
  bg: "#f7fafc",
  card: "#ffffff",
  border: "#e2e8f0",
};

const CHART_COLORS = ["#1a365d", "#2b6cb0", "#3182ce", "#e53e3e", "#ed8936", "#38a169", "#805ad5", "#d69e2e"];
const PIE_COLORS = ["#1a365d", "#2b6cb0", "#e53e3e", "#38a169", "#805ad5"];

const fmt = (v) => \`$\${(v / 1000).toFixed(0)}K\`;
const fmtFull = (v) => \`$\${v.toLocaleString("en-US", { minimumFractionDigits: 0, maximumFractionDigits: 0 })}\`;
const fmtCount = (v) => v.toLocaleString("en-US");

const monthlyData = [
  { month: "Jan", revenue: 458545, transactions: 127677 },
  { month: "Feb", revenue: 413895, transactions: 115248 },
  { month: "Mar", revenue: 456643, transactions: 127147 },
  { month: "Apr", revenue: 442842, transactions: 123302 },
  { month: "May", revenue: 458194, transactions: 127581 },
  { month: "Jun", revenue: 443228, transactions: 123415 },
  { month: "Jul", revenue: 454935, transactions: 126664 },
  { month: "Aug", revenue: 456703, transactions: 127168 },
  { month: "Sep", revenue: 441415, transactions: 122909 },
  { month: "Oct", revenue: 457741, transactions: 127459 },
  { month: "Nov", revenue: 443495, transactions: 123488 },
  { month: "Dec", revenue: 442899, transactions: 123325 },
];

const regionData = [
  { region: "Midwest", revenue: 1136605, transactions: 316472, avgOrder: 3.59 },
  { region: "Southeast", revenue: 1076205, transactions: 299656, avgOrder: 3.59 },
  { region: "Mid-Atlantic", revenue: 945971, transactions: 263407, avgOrder: 3.59 },
  { region: "West", revenue: 783160, transactions: 218070, avgOrder: 3.59 },
  { region: "Southwest", revenue: 779935, transactions: 217162, avgOrder: 3.59 },
  { region: "Northeast", revenue: 267594, transactions: 74510, avgOrder: 3.59 },
  { region: "Northwest", revenue: 238657, transactions: 66450, avgOrder: 3.59 },
  { region: "Central", revenue: 142407, transactions: 39656, avgOrder: 3.59 },
];

const paymentData = [
  { method: "Debit Card", revenue: 1345841, transactions: 374727 },
  { method: "Cash", revenue: 1343713, transactions: 374142 },
  { method: "Mobile Pay", revenue: 1337899, transactions: 372531 },
  { method: "Gift Card", revenue: 672063, transactions: 187133 },
  { method: "Credit Card", revenue: 671019, transactions: 186850 },
];

const topStores = [
  { name: "Store #197", city: "Ashland", state: "NC", region: "Southeast", revenue: 18408 },
  { name: "Store #17", city: "Ashland", state: "CA", region: "West", revenue: 18344 },
  { name: "Store #56", city: "Ashland", state: "CO", region: "Central", revenue: 18299 },
  { name: "Store #38", city: "Ashland", state: "CA", region: "West", revenue: 18287 },
  { name: "Store #136", city: "Ashland", state: "MA", region: "Northeast", revenue: 18286 },
  { name: "Store #169", city: "Ashland", state: "NJ", region: "Mid-Atlantic", revenue: 18280 },
  { name: "Store #126", city: "Ashland", state: "ME", region: "Northeast", revenue: 18274 },
  { name: "Store #2", city: "Ashland", state: "AL", region: "Southeast", revenue: 18262 },
  { name: "Store #184", city: "Ashland", state: "NY", region: "Mid-Atlantic", revenue: 18222 },
  { name: "Store #144", city: "Ashland", state: "MI", region: "Midwest", revenue: 18218 },
];

const topStates = [
  { state: "CA", revenue: 712616 },
  { state: "TX", revenue: 531794 },
  { state: "FL", revenue: 355413 },
  { state: "NY", revenue: 321167 },
  { state: "PA", revenue: 214527 },
  { state: "IL", revenue: 213448 },
  { state: "OH", revenue: 194630 },
  { state: "MI", revenue: 160327 },
  { state: "NC", revenue: 142869 },
  { state: "NJ", revenue: 142531 },
];

const loyaltyData = [
  { tier: "Bronze", customers: 41828, avgSpend: 75.40, avgTx: 21.0 },
  { tier: "Silver", customers: 33329, avgSpend: 75.37, avgTx: 21.0 },
  { tier: "Gold", customers: 16491, avgSpend: 75.35, avgTx: 21.0 },
  { tier: "Platinum", customers: 8352, avgSpend: 75.51, avgTx: 21.0 },
];

const TOTAL_REVENUE = monthlyData.reduce((s, d) => s + d.revenue, 0);
const TOTAL_TX = monthlyData.reduce((s, d) => s + d.transactions, 0);
const AVG_ORDER = TOTAL_REVENUE / TOTAL_TX;

const KPICard = ({ label, value, sub, accent }) => (
  <div style={{
    background: BRAND.card,
    borderRadius: 12,
    padding: "20px 24px",
    borderLeft: \`4px solid \${accent || BRAND.accent}\`,
    boxShadow: "0 1px 3px rgba(0,0,0,0.06)",
    flex: 1,
    minWidth: 180,
  }}>
    <div style={{ fontSize: 12, fontWeight: 600, color: BRAND.textLight, textTransform: "uppercase", letterSpacing: "0.05em", marginBottom: 6 }}>{label}</div>
    <div style={{ fontSize: 28, fontWeight: 700, color: BRAND.primary, lineHeight: 1.1 }}>{value}</div>
    {sub && <div style={{ fontSize: 12, color: BRAND.textLight, marginTop: 4 }}>{sub}</div>}
  </div>
);

const SectionTitle = ({ children }) => (
  <h3 style={{ fontSize: 15, fontWeight: 700, color: BRAND.primary, margin: "0 0 16px 0", letterSpacing: "-0.01em" }}>{children}</h3>
);

const ChartCard = ({ children, style }) => (
  <div style={{
    background: BRAND.card,
    borderRadius: 12,
    padding: 24,
    boxShadow: "0 1px 3px rgba(0,0,0,0.06)",
    border: \`1px solid \${BRAND.border}\`,
    ...style,
  }}>
    {children}
  </div>
);

const CustomTooltip = ({ active, payload, label, formatter }) => {
  if (!active || !payload?.length) return null;
  return (
    <div style={{
      background: "#fff",
      border: \`1px solid \${BRAND.border}\`,
      borderRadius: 8,
      padding: "10px 14px",
      boxShadow: "0 4px 12px rgba(0,0,0,0.1)",
      fontSize: 13,
    }}>
      <div style={{ fontWeight: 700, color: BRAND.primary, marginBottom: 4 }}>{label}</div>
      {payload.map((p, i) => (
        <div key={i} style={{ color: p.color || BRAND.text, marginTop: 2 }}>
          {p.name}: {formatter ? formatter(p.value) : p.value}
        </div>
      ))}
    </div>
  );
};

const tabs = ["Overview", "Regions", "Payment & Loyalty"];

export default function Dashboard() {
  const [activeTab, setActiveTab] = useState("Overview");
  const [metric, setMetric] = useState("revenue");

  return (
    <div style={{
      fontFamily: "'Inter', -apple-system, sans-serif",
      background: BRAND.bg,
      minHeight: "100vh",
      color: BRAND.text,
    }}>
      <style>{\`
        @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&display=swap');
        * { box-sizing: border-box; margin: 0; padding: 0; }
        ::-webkit-scrollbar { width: 6px; }
        ::-webkit-scrollbar-track { background: transparent; }
        ::-webkit-scrollbar-thumb { background: #cbd5e0; border-radius: 3px; }
      \`}</style>

      {/* Header */}
      <div style={{
        background: BRAND.primary,
        padding: "20px 32px",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
      }}>
        <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
          <div style={{
            width: 36, height: 36, borderRadius: 8,
            background: BRAND.secondary,
            display: "flex", alignItems: "center", justifyContent: "center",
            fontWeight: 800, color: "#fff", fontSize: 16,
          }}>A</div>
          <div>
            <div style={{ color: "#fff", fontSize: 18, fontWeight: 700, letterSpacing: "-0.02em" }}>ACME Corp</div>
            <div style={{ color: "rgba(255,255,255,0.6)", fontSize: 12, fontWeight: 500 }}>Sales Intelligence Dashboard</div>
          </div>
        </div>
        <div style={{
          background: "rgba(255,255,255,0.12)",
          borderRadius: 8,
          padding: "6px 14px",
          color: "rgba(255,255,255,0.8)",
          fontSize: 13,
          fontWeight: 500,
        }}>
          FY 2025
        </div>
      </div>

      {/* Tab Bar */}
      <div style={{
        background: BRAND.card,
        borderBottom: \`1px solid \${BRAND.border}\`,
        padding: "0 32px",
        display: "flex",
        gap: 0,
      }}>
        {tabs.map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            style={{
              padding: "14px 20px",
              fontSize: 13,
              fontWeight: 600,
              background: "none",
              border: "none",
              cursor: "pointer",
              color: activeTab === tab ? BRAND.primary : BRAND.textLight,
              borderBottom: activeTab === tab ? \`2px solid \${BRAND.secondary}\` : "2px solid transparent",
              transition: "all 0.15s",
            }}
          >
            {tab}
          </button>
        ))}
      </div>

      {/* Content */}
      <div style={{ padding: "24px 32px", maxWidth: 1280, margin: "0 auto" }}>

        {activeTab === "Overview" && (
          <>
            {/* KPIs */}
            <div style={{ display: "flex", gap: 16, marginBottom: 24, flexWrap: "wrap" }}>
              <KPICard label="Total Revenue" value={\`$\${(TOTAL_REVENUE / 1e6).toFixed(2)}M\`} sub="Jan–Dec 2025" accent={BRAND.accent} />
              <KPICard label="Transactions" value={\`\${(TOTAL_TX / 1e6).toFixed(2)}M\`} sub="Across 500 stores" accent="#38a169" />
              <KPICard label="Avg Order Value" value={\`$\${AVG_ORDER.toFixed(2)}\`} sub="Per transaction" accent="#805ad5" />
              <KPICard label="Top Region" value="Midwest" sub={\`\${fmtFull(1136605)} revenue\`} accent={BRAND.secondary} />
            </div>

            {/* Monthly Trend */}
            <ChartCard style={{ marginBottom: 24 }}>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
                <SectionTitle>Monthly Revenue & Transactions</SectionTitle>
                <div style={{ display: "flex", gap: 4, background: BRAND.bg, borderRadius: 6, padding: 2 }}>
                  {["revenue", "transactions"].map((m) => (
                    <button
                      key={m}
                      onClick={() => setMetric(m)}
                      style={{
                        padding: "5px 12px",
                        fontSize: 12,
                        fontWeight: 600,
                        border: "none",
                        borderRadius: 4,
                        cursor: "pointer",
                        background: metric === m ? BRAND.primary : "transparent",
                        color: metric === m ? "#fff" : BRAND.textLight,
                        transition: "all 0.15s",
                        textTransform: "capitalize",
                      }}
                    >
                      {m}
                    </button>
                  ))}
                </div>
              </div>
              <ResponsiveContainer width="100%" height={280}>
                <AreaChart data={monthlyData} margin={{ top: 8, right: 8, left: 8, bottom: 0 }}>
                  <defs>
                    <linearGradient id="colorRev" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor={BRAND.accent} stopOpacity={0.2} />
                      <stop offset="95%" stopColor={BRAND.accent} stopOpacity={0} />
                    </linearGradient>
                    <linearGradient id="colorTx" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#38a169" stopOpacity={0.2} />
                      <stop offset="95%" stopColor="#38a169" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#edf2f7" />
                  <XAxis dataKey="month" tick={{ fontSize: 12, fill: BRAND.textLight }} axisLine={false} tickLine={false} />
                  <YAxis
                    tick={{ fontSize: 12, fill: BRAND.textLight }}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={metric === "revenue" ? fmt : fmtCount}
                  />
                  <Tooltip content={<CustomTooltip formatter={metric === "revenue" ? fmtFull : fmtCount} />} />
                  {metric === "revenue" ? (
                    <Area type="monotone" dataKey="revenue" name="Revenue" stroke={BRAND.accent} fill="url(#colorRev)" strokeWidth={2.5} dot={{ r: 4, fill: BRAND.accent, stroke: "#fff", strokeWidth: 2 }} />
                  ) : (
                    <Area type="monotone" dataKey="transactions" name="Transactions" stroke="#38a169" fill="url(#colorTx)" strokeWidth={2.5} dot={{ r: 4, fill: "#38a169", stroke: "#fff", strokeWidth: 2 }} />
                  )}
                </AreaChart>
              </ResponsiveContainer>
            </ChartCard>

            {/* Top Stores & Top States side-by-side */}
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 24 }}>
              <ChartCard>
                <SectionTitle>Top 10 Stores by Revenue</SectionTitle>
                <div style={{ fontSize: 13 }}>
                  {topStores.map((s, i) => (
                    <div key={i} style={{
                      display: "flex", alignItems: "center", justifyContent: "space-between",
                      padding: "8px 0",
                      borderBottom: i < topStores.length - 1 ? \`1px solid \${BRAND.border}\` : "none",
                    }}>
                      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                        <span style={{
                          width: 22, height: 22, borderRadius: 6,
                          background: i < 3 ? BRAND.secondary : BRAND.bg,
                          color: i < 3 ? "#fff" : BRAND.textLight,
                          display: "flex", alignItems: "center", justifyContent: "center",
                          fontSize: 11, fontWeight: 700, flexShrink: 0,
                        }}>{i + 1}</span>
                        <div>
                          <div style={{ fontWeight: 600, color: BRAND.primary }}>{s.name}</div>
                          <div style={{ fontSize: 11, color: BRAND.textLight }}>{s.city}, {s.state} · {s.region}</div>
                        </div>
                      </div>
                      <div style={{ fontWeight: 700, color: BRAND.text, fontVariantNumeric: "tabular-nums" }}>{fmtFull(s.revenue)}</div>
                    </div>
                  ))}
                </div>
              </ChartCard>

              <ChartCard>
                <SectionTitle>Top States by Revenue</SectionTitle>
                <ResponsiveContainer width="100%" height={380}>
                  <BarChart data={topStates} layout="vertical" margin={{ top: 0, right: 16, left: 8, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#edf2f7" horizontal={false} />
                    <XAxis type="number" tick={{ fontSize: 11, fill: BRAND.textLight }} axisLine={false} tickLine={false} tickFormatter={fmt} />
                    <YAxis type="category" dataKey="state" tick={{ fontSize: 12, fill: BRAND.text, fontWeight: 600 }} axisLine={false} tickLine={false} width={32} />
                    <Tooltip content={<CustomTooltip formatter={fmtFull} />} />
                    <Bar dataKey="revenue" name="Revenue" fill={BRAND.accent} radius={[0, 4, 4, 0]} barSize={20} />
                  </BarChart>
                </ResponsiveContainer>
              </ChartCard>
            </div>
          </>
        )}

        {activeTab === "Regions" && (
          <>
            <ChartCard style={{ marginBottom: 24 }}>
              <SectionTitle>Revenue by Region</SectionTitle>
              <ResponsiveContainer width="100%" height={340}>
                <BarChart data={regionData} margin={{ top: 8, right: 16, left: 8, bottom: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#edf2f7" />
                  <XAxis dataKey="region" tick={{ fontSize: 11, fill: BRAND.textLight }} axisLine={false} tickLine={false} />
                  <YAxis tick={{ fontSize: 11, fill: BRAND.textLight }} axisLine={false} tickLine={false} tickFormatter={fmt} />
                  <Tooltip content={<CustomTooltip formatter={fmtFull} />} />
                  <Bar dataKey="revenue" name="Revenue" radius={[6, 6, 0, 0]} barSize={48}>
                    {regionData.map((_, i) => (
                      <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </ChartCard>

            <ChartCard>
              <SectionTitle>Region Detail</SectionTitle>
              <div style={{ overflowX: "auto" }}>
                <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
                  <thead>
                    <tr style={{ borderBottom: \`2px solid \${BRAND.border}\` }}>
                      {["Region", "Revenue", "Transactions", "Avg Order", "Share"].map((h) => (
                        <th key={h} style={{
                          textAlign: h === "Region" ? "left" : "right",
                          padding: "10px 12px",
                          fontWeight: 700,
                          color: BRAND.textLight,
                          fontSize: 11,
                          textTransform: "uppercase",
                          letterSpacing: "0.05em",
                        }}>{h}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {regionData.map((r, i) => {
                      const share = ((r.revenue / TOTAL_REVENUE) * 100).toFixed(1);
                      return (
                        <tr key={i} style={{ borderBottom: \`1px solid \${BRAND.border}\` }}>
                          <td style={{ padding: "10px 12px", fontWeight: 600, color: BRAND.primary }}>
                            <span style={{ display: "inline-block", width: 10, height: 10, borderRadius: 3, background: CHART_COLORS[i], marginRight: 8, verticalAlign: "middle" }} />
                            {r.region}
                          </td>
                          <td style={{ padding: "10px 12px", textAlign: "right", fontWeight: 600, fontVariantNumeric: "tabular-nums" }}>{fmtFull(r.revenue)}</td>
                          <td style={{ padding: "10px 12px", textAlign: "right", color: BRAND.textLight, fontVariantNumeric: "tabular-nums" }}>{fmtCount(r.transactions)}</td>
                          <td style={{ padding: "10px 12px", textAlign: "right", color: BRAND.textLight }}>\${r.avgOrder.toFixed(2)}</td>
                          <td style={{ padding: "10px 12px", textAlign: "right" }}>
                            <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 8 }}>
                              <div style={{ width: 60, height: 6, background: "#edf2f7", borderRadius: 3, overflow: "hidden" }}>
                                <div style={{ width: \`\${share}%\`, height: "100%", background: CHART_COLORS[i], borderRadius: 3 }} />
                              </div>
                              <span style={{ fontWeight: 600, fontSize: 12, minWidth: 36 }}>{share}%</span>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </ChartCard>
          </>
        )}

        {activeTab === "Payment & Loyalty" && (
          <>
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 24, marginBottom: 24 }}>
              <ChartCard>
                <SectionTitle>Payment Method Revenue</SectionTitle>
                <ResponsiveContainer width="100%" height={280}>
                  <PieChart>
                    <Pie
                      data={paymentData}
                      dataKey="revenue"
                      nameKey="method"
                      cx="50%"
                      cy="50%"
                      innerRadius={65}
                      outerRadius={110}
                      paddingAngle={3}
                      strokeWidth={0}
                    >
                      {paymentData.map((_, i) => (
                        <Cell key={i} fill={PIE_COLORS[i]} />
                      ))}
                    </Pie>
                    <Tooltip formatter={(v) => fmtFull(v)} />
                  </PieChart>
                </ResponsiveContainer>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 12, justifyContent: "center", marginTop: 8 }}>
                  {paymentData.map((p, i) => (
                    <div key={i} style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12 }}>
                      <span style={{ width: 10, height: 10, borderRadius: 3, background: PIE_COLORS[i] }} />
                      <span style={{ fontWeight: 600, color: BRAND.text }}>{p.method}</span>
                      <span style={{ color: BRAND.textLight }}>{((p.revenue / TOTAL_REVENUE) * 100).toFixed(1)}%</span>
                    </div>
                  ))}
                </div>
              </ChartCard>

              <ChartCard>
                <SectionTitle>Payment Method Transactions</SectionTitle>
                <ResponsiveContainer width="100%" height={300}>
                  <BarChart data={paymentData} margin={{ top: 8, right: 8, left: 8, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#edf2f7" />
                    <XAxis dataKey="method" tick={{ fontSize: 11, fill: BRAND.textLight }} axisLine={false} tickLine={false} />
                    <YAxis tick={{ fontSize: 11, fill: BRAND.textLight }} axisLine={false} tickLine={false} tickFormatter={(v) => \`\${(v / 1000).toFixed(0)}K\`} />
                    <Tooltip content={<CustomTooltip formatter={fmtCount} />} />
                    <Bar dataKey="transactions" name="Transactions" radius={[6, 6, 0, 0]} barSize={40}>
                      {paymentData.map((_, i) => (
                        <Cell key={i} fill={PIE_COLORS[i]} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </ChartCard>
            </div>

            <ChartCard>
              <SectionTitle>Customer Loyalty Tiers</SectionTitle>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 16, marginBottom: 20 }}>
                {loyaltyData.map((t, i) => {
                  const tierColors = { Bronze: "#b7791f", Silver: "#718096", Gold: "#d69e2e", Platinum: "#805ad5" };
                  return (
                    <div key={i} style={{
                      background: BRAND.bg,
                      borderRadius: 10,
                      padding: 18,
                      borderTop: \`3px solid \${tierColors[t.tier]}\`,
                    }}>
                      <div style={{ fontSize: 12, fontWeight: 700, color: tierColors[t.tier], textTransform: "uppercase", letterSpacing: "0.05em" }}>{t.tier}</div>
                      <div style={{ fontSize: 26, fontWeight: 800, color: BRAND.primary, marginTop: 6 }}>{fmtCount(t.customers)}</div>
                      <div style={{ fontSize: 11, color: BRAND.textLight, marginTop: 2 }}>members</div>
                      <div style={{ borderTop: \`1px solid \${BRAND.border}\`, marginTop: 10, paddingTop: 10, display: "flex", justifyContent: "space-between" }}>
                        <div>
                          <div style={{ fontSize: 11, color: BRAND.textLight }}>Avg Spend</div>
                          <div style={{ fontSize: 14, fontWeight: 700, color: BRAND.text }}>\${t.avgSpend.toFixed(2)}</div>
                        </div>
                        <div>
                          <div style={{ fontSize: 11, color: BRAND.textLight }}>Avg Txns</div>
                          <div style={{ fontSize: 14, fontWeight: 700, color: BRAND.text }}>{t.avgTx.toFixed(1)}</div>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
              <div style={{ display: "flex", gap: 6, alignItems: "flex-end", height: 120, padding: "0 16px" }}>
                {loyaltyData.map((t, i) => {
                  const tierColors = { Bronze: "#b7791f", Silver: "#718096", Gold: "#d69e2e", Platinum: "#805ad5" };
                  const maxC = Math.max(...loyaltyData.map((d) => d.customers));
                  const h = (t.customers / maxC) * 100;
                  return (
                    <div key={i} style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", gap: 4 }}>
                      <div style={{ fontSize: 11, fontWeight: 600, color: BRAND.textLight }}>{((t.customers / 100000) * 100).toFixed(1)}%</div>
                      <div style={{
                        width: "70%",
                        height: \`\${h}%\`,
                        background: tierColors[t.tier],
                        borderRadius: "4px 4px 0 0",
                        transition: "height 0.3s",
                        opacity: 0.85,
                      }} />
                      <div style={{ fontSize: 11, fontWeight: 600, color: BRAND.text }}>{t.tier}</div>
                    </div>
                  );
                })}
              </div>
            </ChartCard>
          </>
        )}

        {/* Footer */}
        <div style={{
          textAlign: "center",
          padding: "24px 0 12px",
          fontSize: 11,
          color: BRAND.textLight,
        }}>
          ACME Corp · America's Trusted Retailer · Data sourced from OpenSearch Analytics Index · FY 2025
        </div>
      </div>
    </div>
  );
}`;
