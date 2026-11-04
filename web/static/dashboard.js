// Draws dashboard trends with uPlot: income vs expense bars per month with a net worth
// line on its own scale and right axis. Each figure.trend holds its series as JSON, see
// ui.trendChart.
(() => {
  const halfMonth = 15 * 24 * 60 * 60;
  // the page language, see layout.html
  const locale = document.documentElement.lang || undefined;
  const compact = new Intl.NumberFormat(locale, { notation: 'compact' });
  // whole units unless the user shows minor units, see the display setting
  const full = new Intl.NumberFormat(locale, {
    maximumFractionDigits: document.body.hasAttribute('data-minor-units') ? 2 : 0,
  });
  let charts = [];

  const css = (name) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

  function bars(color, align) {
    return {
      stroke: color,
      fill: color,
      width: 0,
      paths: uPlot.paths.bars({ size: [0.4, 40], align }),
      points: { show: false },
      value: (u, v) => (v == null ? '' : full.format(v)),
    };
  }

  function render() {
    charts.forEach((c) => c.destroy());
    charts = [];
    document.querySelectorAll('figure.trend').forEach((fig) => {
      const data = fig.querySelector('script');
      const d = JSON.parse(data.textContent);
      // translated series labels, see dashboard.html
      const label = data.dataset;
      const networth = d.balance != null;
      const axis = { stroke: css('--pico-muted-color'), grid: { stroke: css('--pico-muted-border-color'), width: 1 }, ticks: { show: false } };
      // With no cursor over the chart the legend shows the last (current) month.
      const current = (u) => { if (u.cursor.idx == null) u.setLegend({ idx: u.data[0].length - 1 }); };
      charts.push(new uPlot({
        width: fig.clientWidth,
        height: 220,
        cursor: { points: { show: false }, drag: { x: false, y: false } },
        hooks: { ready: [current], setCursor: [current] },
        scales: { x: { time: true, range: (u, min, max) => [min - halfMonth, max + halfMonth] } },
        axes: [
          { ...axis, grid: { show: false }, splits: (u) => u.data[0], values: (u, ts) => ts.map((t) => new Date(t * 1000).toLocaleDateString(locale, { month: 'short', timeZone: 'UTC' })) },
          { ...axis, size: 50, values: (u, vs) => vs.map((v) => compact.format(v)) },
          // net worth dwarfs a month of income or expense: its own scale, no grid
          ...(networth ? [{ ...axis, scale: 'networth', side: 1, size: 50, grid: { show: false }, values: (u, vs) => vs.map((v) => compact.format(v)) }] : []),
        ],
        series: [
          { value: (u, t) => (t == null ? '' : new Date(t * 1000).toLocaleDateString(locale, { month: 'long', year: 'numeric', timeZone: 'UTC' })) },
          { label: label.labelIncome || 'Income', ...bars(css('--gf-income'), -1) },
          { label: label.labelExpense || 'Expense', ...bars(css('--gf-expense'), 1) },
          ...(networth ? [{ label: label.labelBalance || 'Net worth', scale: 'networth', stroke: css('--pico-primary'), width: 2, points: { size: 5 }, value: (u, v) => (v == null ? '' : full.format(v)) }] : []),
        ],
      }, networth ? [d.months, d.income, d.expense, d.balance] : [d.months, d.income, d.expense], fig));
    });
  }

  document.addEventListener('DOMContentLoaded', render);
  document.addEventListener('htmx:after:settle', render);
  // colors are read from CSS once per render: redraw when the OS theme flips
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', render);
  window.addEventListener('resize', () => {
    charts.forEach((c) => c.setSize({ width: c.root.parentElement.clientWidth, height: 220 }));
  });
})();
