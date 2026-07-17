# Deep Research: Kalshi Tennis Market Trading

## What We're Doing

Ghost trader: real-time WebSocket scraper for Kalshi tennis match-winner markets. Stores every tick, trade, orderbook delta, and lifecycle event to SQLite. Optional real-time score feed from API-Tennis via WebSocket. Pluggable strategy framework for backtesting algorithms against historical data.

Core thesis: tennis match markets on Kalshi are inefficient enough to exploit using point-by-point score data combined with orderbook dynamics. The edge comes from having faster/better information than the average market participant.

---

## 1. Academic Papers — Tennis Match Modeling

### Klaassen & Magnus (2001) — "Are Points in Tennis Independent and Identically Distributed?"
- **Paper**: http://www.janmagnus.nl/papers/JRM057.pdf
- **Journal**: JASA, Vol 96, No 454
- **Data**: 90,000 points from Wimbledon 1992-1995
- **Key finding**: Points are NOT iid. Winning previous point increases win probability of current point by ~0.3% (men) / ~0.5% (women). Important points (break points) are harder for server — up to 4.6% lower serve-win probability at 30-40 in 5th set.
- **However**: Deviations from iid are small. Iid model is a good first-order approximation.
- **Relevance**: Our Markov chain model can use iid as baseline. Point-level momentum effects exist and could be exploited.

### Magnus & Klaassen (2003) — "Forecasting the Winner of a Tennis Match"
- **Paper**: https://www.janmagnus.nl/papers/JRM065.pdf
- **Journal**: European Journal of Operational Research
- **Key contribution**: TENNISPROB program — computes exact match win probability (not simulation) given serve probabilities, current score, server, tournament format.
- **Relevance**: Directly applicable to our fair-value model. Compute theoretical fair price from current score state, compare to Kalshi market price.

### Newton & Aslam (2009) — "Monte Carlo Tennis: A Stochastic Markov Chain Model"
- **Paper**: https://doi.org/10.2202/1559-0410.1169
- **Journal**: JQAS, Vol 5, Issue 3
- **Key contribution**: Markov chain model using 4 parameters per player (mean/std of serve-win and return-win probabilities). Monte Carlo simulation for match outcome PDFs.
- **Relevance**: Alternative to exact computation — useful for quick probability estimates during live matches.

### Ingram (2018) — "A Point-Based Bayesian Hierarchical Model"
- **Paper**: https://martiningram.github.io/papers/bayes_point_based.pdf
- **Journal**: JQAS
- **Key finding**: Bayesian hierarchical model with Gaussian random walk for player skills over time, surface-specific adjustments. 68.8% accuracy on ATP 2014 (vs 66.3% for previous best point-based). Competitive with Elo models.
- **Relevance**: Best-in-class point-based model. Could serve as pre-match fair value baseline.

### "The Winning Probability of a Game and the Importance of Points" (2019)
- **Paper**: https://doi.org/10.1080/02701367.2019.1666203
- **Key finding**: DTMC model. Server's point-winning probability varies significantly by score state. Independence generally holds except at 40-15.
- **Relevance**: Score-state-dependent serve probabilities improve model accuracy.

### Kovalchik (2016) — "Searching for the GOAT of Tennis Win Prediction"
- **Journal**: JQAS, Vol 12, Issue 3
- **Key finding**: Compared 11 published tennis prediction models. Elo with custom k-factor (FiveThirtyEight) was best. Point-based models underperformed regression and paired-comparison models.
- **Relevance**: Important baseline. If building a point-based model, need to beat Elo to claim edge.

---

## 2. Market Making Theory

### Avellaneda & Stoikov (2008) — "High-Frequency Trading in a Limit Order Book"
- Canonical model for inventory-aware market making.
- **Core formulas**:
  - Reservation price: `r = s - q * gamma * sigma^2 * (T-t)`
  - Optimal spread: `delta = gamma * sigma^2 * (T-t) + (2/gamma) * ln(1 + gamma/kappa)`
  - Bid = r - delta/2, Ask = r + delta/2
- **Key insight**: Quote around a private fair value that skews AWAY from inventory. Wider spreads when volatility or risk aversion is high.
- **Implementation references**:
  - Hummingbot guide: https://hummingbot.org/blog/guide-to-the-avellaneda--stoikov-strategy/
  - Stanford lecture notes: https://stanford.edu/~ashlearn/RLForFinanceBook/MarketMaking.pdf
  - HFT Book chapter: https://hftradingbook.com/strategies/avellaneda-stoikov
  - Interactive simulator: https://market-makers.streamlit.app/
  - Maverick Quant deep dive: https://maverickquant.substack.com/p/liquidity-provision-market-making

### Adaptation to Prediction Markets / Binary Options
- **TruF Network market maker**: https://github.com/truflation/market-maker-bot
  - A-S adapted for binary options. Black-Scholes for initial pricing when no order book exists.
  - Volatility from RMS of consecutive mid-price differences.
- **Horizon SDK**: Rust-native A-S for prediction markets.
  - Competitive spread blending (model vs live orderbook). Multi-level quoting with size decay.

### Prediction Market MM — Academic
- **"A General Theory of Liquidity Provisioning for Prediction Markets"** (2023): https://arxiv.org/pdf/2311.08725
- **"Price Evolution in a CDA Prediction Market with Scoring-Rule MM"** (AAAI): LMSR in continuous double auction. Lower spreads but doesn't necessarily improve price discovery.
- **"Integrating Market Makers, Limit Orders, and Continuous Trade"** (2015): First concrete algorithm combining MMs and limit orders in prediction markets.
- **"Arbitrage-Free Combinatorial Market Making via IP"**: Frank-Wolfe market maker. Bounded loss, arbitrage removal.

### Paradigm Prediction Market Challenge — #2 Finisher
- **Repo**: https://github.com/octavi42/prediction-market-maker
- **Key discoveries**:
  1. **Monopoly regime**: When competitor quotes vanish on one side (prob near 0 or 1), become sole liquidity provider. 60% of total edge. Flipped strategy from -$24/sim to +$40/sim.
  2. **Size = 85/prob in monopoly**: Aggressive sizing when only provider.
  3. **Retail-matching sizing**: `size = 14/prob` to match expected retail fill (~$4.5 mean notional). Excess orders get swept by arb.
  4. **Inventory skew is make-or-break**: Removing skew drops score by $7. Formula: `min(0.08, 2.8/size)`.
  5. **Z-score filtering**: Don't quote when spread doesn't justify risk. Saves ~$5/sim.
- **Directly relevant**: Closest published work to our setup — MM on binary prediction markets with adversarial arbitrageur.

---

## 3. Reinforcement Learning for Tennis Market Making

### William Devena — MSc Thesis: "RL for Market Making in Algorithmic Sports Trading"
- **Repo**: https://github.com/williamdevena/RL_for_Market_making_in_Sports_Trading
- **Focus**: Tennis betting markets on Betfair.
- **Three experiments**:
  1. Analysis of Betfair exchange data (volumes, volatility, liquidity, pre-game vs in-play).
  2. Avellaneda-Stoikov baseline adapted to sports trading. Novel simulation framework.
  3. RL agents (DQN, PPO, A2C) trained to surpass A-S baseline. Evaluated on PnL, Sharpe, Sortino.
- **Tennis Markov chain modules**: Probabilities for matches, sets, games, tiebreaks. Simulates price time series.
- **Relevance**: Most directly related academic work. Proves A-S can be adapted to tennis, RL can improve on it.

---

## 4. Real-Time Tennis Data Sources

### Commercial APIs
- **Tennis API (tennis-api.com)**: Point-by-point, WebSocket real-time. $99/month+. Sub-second latency. ATP/WTA/ITF/Challenger.
- **SportsAPI Pro**: `/api/match/{matchId}/point-by-point`. Serve, return, break points.
- **API 4 Sports**: Live scores, point-by-point, odds (pre-match + in-play).
- **Stats Perform (Official WTA)**: Ultrafast umpire feed + shot-by-shot. RunningBall: 80% of points faster than umpire feed.
- **Genius Sports**: Automated tennis trading with "Smart Suspend", "Key Point Boost", "Confidence Parameter". Outperforms exchange market pricing.

### In-Play Tennis Trading Mechanics (Betfair)
- **Source**: https://betfairsquare.com/sports/tennis-in-play-strategies
- Match Odds prices swing 30-100 ticks per game. Break point converted = 60-120 tick move in 8 seconds.
- ATP hold serve: 78-85% hard, 75-82% clay, 85-92% grass. WTA: 60-72%.
- **Key patterns**:
  - Markets OVER-REACT to break points and UNDER-REACT to break-backs.
  - Back favourite at 0-40 down on serve (price spikes to ~1.85, recovers to ~1.45 if hold).
  - Lay the breaking player (fade the spike). Breaker still must hold next game.
  - Tiebreaks: highest volatility. Mini-breaks are mean-reverting.
- **Latency warning**: TV/stream lag 10-20 seconds. Tournament data feeds (Sportradar, Stats Perform) 0.5-1.5s. Betfair imposes 5-second betting delay in-play.
- **Relevance**: These patterns directly inform what our strategy should look for in Kalshi tennis markets.

### Tennis Trader / Bet Angel
- Commercial tennis trading software with "bookmaker grade" odds model.
- Predicts odds movement from serve percentage data.
- Live scores from umpire chair streamed into trading desk.
- **Relevance**: Validates that serve-percentage-based odds modeling is the industry standard approach.

---

## 5. Existing Kalshi Trading Bots and Projects

### Market Making Bots
- **rodlaf/KalshiMarketMaker**: https://github.com/rodlaf/KalshiMarketMaker
  - Avellaneda-Stoikov per selected market. Dynamic market selection with volume/spread scoring.
  - Portfolio controls: global + per-market contract caps. Aggressive liquidation mode.
  - Config: gamma=0.2, kappa=1.5, sigma=0.001, T=28800, min_spread=0.02.
  - **WARNING**: sigma=0.001 is likely wrong for binary options (should be 0.05-0.15). See Rodney L. postmortem below.

### AI/LLM Trading Bots
- **ryanfrigo/kalshi-ai-trading-bot**: AI-automated strategies. LLM directional scoring, Kelly sizing, SQLite telemetry, Streamlit dashboard. Safe Compounder (NO-side edge strategy). Authors warn: "example strategies lose money."
- **openfi-dao/kalshi-trading-bot**: 5-agent LLM ensemble. Forecaster (30%), News Analyst (20%), Bull (20%), Bear (15%), Risk Manager (15%). Kelly sizing with disagreement penalty.
- **cdavisv/kalshi-ai-trading-bot**: Multi-model (Claude, Gemini, GPT, DeepSeek, Grok). Found NCAAB NO-side trading profitable (74% win rate, +10% ROI). Category scoring blocks proven negative-edge categories.

### Arbitrage Scanners
- **mathslug/Karb_Scanner**: https://github.com/mathslug/Karb_Scanner
  - Cross-market arbitrage for Kalshi tennis and hockey. LLM (Claude) for logical implication inference.
  - Example: "Djokovic wins French Open" at 8c but "Djokovic wins Grand Slam" at 5c = arb.
  - Human-in-the-loop review. LLM good but not perfect at implication direction.
- **axwelbrand-byte/ArbiBot**: Cross-platform (Kalshi + Polymarket) sports arb bot. Tennis included. 70% near-term / 30% future allocation. Paper trading mode.
- **oleksandrbannick/Meridian**: Full-stack Kalshi sports trading terminal. Arb scanner, middle-spread scanner, bot system with auto-monitor.

### Framework Bots
- **Viprasol-Tech/kalshi-trading-bot**: Python framework. 5 bundled strategies (MM, momentum, mean_reversion, arbitrage, fair_value). Backtester with Sharpe/drawdown metrics. Dry-run by default.

---

## 6. Cross-Platform Arbitrage (Kalshi + Polymarket)

### State of Arb Opportunities
- **Average arb duration**: 2.7 seconds (down from 12.3s in 2024). 73% of profits captured by sub-100ms bots.
- **Minimum profitable spread**: 2.5% after fees (up from 1.8%).
- **Fee structure**: Kalshi taker fee ~1.75% max. Maker fee 4x less (0.0175 multiplier).
- **Research**: ~$40M in arb profits extracted from Polymarket Apr 2024-Apr 2025. Top 3 wallets captured $4.4M. (Saguillo et al., arxiv 2025)
- **Settlement divergence**: Polymarket 24-48h settlement vs Kalshi 0-4h. Creates 12-24h window where identical contracts trade at different prices.

### Basis Risk Arbitrage
- **Paper**: https://www.researchgate.net/publication/407548020
- "Basis risk arbitrage" — exploiting definitional divergences in how platforms resolve same event.
- Case study: Digital Asset Market Clarity Act. Kalshi Yes + Polymarket No = 5.08% ROI pre-fees, 2.67% after opportunity cost.
- **Relevance**: Even same-event contracts can have different resolution criteria. Must read resolution rules word-for-word on both platforms.

---

## 7. Failure Postmortems — Lessons Learned

### Rodney L. — "$150 Lost in 20 Minutes Market Making on Kalshi"
- **Article**: https://rlafuente.com/posts/2025-3-5-i-lost-150-market-making-on-kalshi
- **6 bugs that caused blowup**:
  1. **Inverted inventory skew**: Missing minus sign. Bot trend-followed its own inventory instead of mean-reverting. Long? Buy more. Short? Sell more.
  2. **Neutered spread**: Spread multiplied by 0.01, collapsing to min_spread floor. Entire A-S model was dead code.
  3. **Sigma off by 100x**: `sigma: 0.001` in config. Binary options need 0.05-0.15. Model thought volatility was zero, quoted razor-thin spreads.
  4. **Market selection preferred wide spreads**: Forgot to invert spread score. Bot sought most illiquid markets.
  5. **Bought MVE combo markets**: No local filter for multivariate event tickers. Untradeable positions.
  6. **top_n was 50**: 50 markets with 20-contract cap. Too thin to earn spread, too spread to manage risk.
- **Lesson**: Paper trade with assertions. Hard MVE filter. Correct sigma. Invert spread score. Fewer markets.

### MAXIMUS (Protogen) — "$35 Lost on Tennis"
- **Article**: https://www.northlakelabs.com/max/blog/how-my-trading-bot-lost-35-on-tennis/
- Ghost systemd service (`protogen-arb-live`) survived shutdown. No category allowlist. Saw wide tennis spreads, thought they were arb edges. Dumped $139 into single tennis match.
- **5 lessons**:
  1. Explicit market category allowlists (default deny).
  2. "Halt trading" requires checklist with exact service names.
  3. Service inventory is mandatory before deploying live capital.
  4. Volume limits per category. Single tennis match should never get $139.
  5. Names matter — don't conflate observation scanner with live executor.

### BotForKalshi — "369 Trades, 0 Wins, $0 P&L"
- **Article**: https://www.botforkalshi.com/blog/distressed-strategy-369-trade-postmortem
- Bought 1-cent OTM contracts expecting 90-cent payout. 369 trades, all expired at entry price. Not a single contract ever moved off 1 cent.
- **3 flaws**:
  1. 1-cent contracts are dead money. Nobody trades them. No price discovery.
  2. Time horizon impossible. 1-cent to 90-cent in 55 minutes = 90x return. Real winners go 5c -> 25c -> 60c over hours.
  3. Exit target unreachable. Single 90-cent target vs tiered exits.
- **5 transferable lessons**:
  1. Pre-register hypothesis with stop criteria. No rule fired after 50 losing trades.
  2. Cheap doesn't mean undervalued. Favorite-longshot bias: longshots systematically overpriced.
  3. Match horizon to required move size.
  4. Tiered exits beat moonshot exits. Sell 1 at 2.5x, 1 at 5x, ride rest.
  5. Build measurement before strategy. If you can't explain bot decisions from logs, you can't fix it.

### Andrew Pierno — "AI Traded Real Money on Kalshi for 60 Days, Lost 25%"
- **Article**: https://blog.andrewpierno.com/i-let-an-ai-trade-real-money-on-kalshi-for-60-days-it-lost-65/
- Gave Claude $250 for weather markets. Lost 26% in 8 days.
- **Mistakes**:
  - Timezone bug: parsed UTC instead of local time. Read yesterday's data, traded today's contracts. $28 lost.
  - Test trades on live config: 5 real orders during debugging. $20 lost.
  - Directional YES bets: 100% wrong. $25 lost.
  - Forecast model trades: 29% win rate. $30 lost.
  - Risk/reward trap: Buying NO at $0.85 means 5.67:1 risk/reward. Need 85% win rate just to break even. Actual was 70-82%.
  - Contaminated data: Mixed timezone-bug-era wins with clean data. Reported 82% win rate. Real was 57%.
- **Key lesson**: A strategy can feel like it's working (winning most days, stable account) and still be slow death by thousand cuts.

### Nick Rae — "Weather Bot: Paper Looked Great, Live Humbled It"
- **Article**: https://nickrae.net/blog/kalshi-weather-bot.html
- Paper: 217 trades, 76.5% win rate, $1,773 profit. Live: paused after model couldn't beat market baseline.
- **What went wrong**:
  - BOUNDARY_FADE strategy: -$19.38/trade average. Disabled.
  - Point forecasts without uncertainty spread: 98F mean means different things at 2F vs 8F ensemble spread.
  - Gate system not strict enough. Passed paper criteria but failed live.
- **Lesson**: Keep in paper/gated mode until model beats market baseline on settled live data. No threshold heroics.

### MAXIMUS — "Weather Market Postmortem: 0 Wins, 32 Losses"
- **Article**: https://www.northlakelabs.com/max/blog/kalshi-weather-postmortem-and-pivot/
- **3 structural failures**:
  1. **Gaussian blindness**: Assumed normal distribution. Weather has fat tails. 2-sigma events happen 10-12% of time, not 5%. Model said 90% certainty, reality was 75-80%.
  2. **Fee death zone**: $0.05 contract with $0.01 fee = 20% immediate tax. Need 83.3% win rate just to break even. Never trade below ~$0.15.
  3. **Exit liquidity**: Weather markets populated by arb bots executing within seconds of NWS updates. 15-60 min polling interval = different universe. Was providing exit liquidity for faster bots.
- **Lesson**: Around trade 10, pattern was visible. Trade 20, unmistakable. Kept running until 32. Kill strategies early.

---

## 8. YouTube Videos and Transcripts

### Strategy and Tutorial Videos

**"I Live Traded Kalshi for 14 Days" — OddsJam**
- URL: https://www.youtube.com/watch?v=Yi2C5zTXh5Y
- $1000 bankroll, $10 unit size. Cross-market EV trading between Kalshi and FanDuel.
- Results: 181-83 record, +16.5 units, $164.92 profit, 5.79% ROI.
- Strategy: Use OddsJam arbitrage tool. Kalshi lines lag behind sportsbooks in live setting. Take Kalshi side of arb plays. 40-cent rule: if line moved more than 40 cents by time you get in, skip it.
- Key insight: Prediction markets lag sharp sportsbooks in live setting. This is the edge.

**"How to Make Money on Kalshi Using Math (3-Step Process)" — Matt Downs**
- URL: https://www.youtube.com/watch?v=t6XzjGJ52sU
- 3-step process: (1) Convert Kalshi cents to American odds. (2) Compare to sharp books (Pinnacle, Circa, Novig). (3) Devig sharp book lines to get true fair probability.
- Gap between Kalshi implied prob and devigged sharp prob = real edge.
- Kalshi is exchange — no account limits for being profitable. Same process got him banned on Hard Rock, Dabble, other DFS apps.
- Uses upside.tools Game Line Optimizer for automated devig + comparison.

**"Kalshi Strategies Explained (Full Breakdown) Part 2" — SmartStake**
- URL: https://www.youtube.com/watch?v=cMKclpJ6gD8
- Market maker vs taker explanation. Limit orders = maker (4x lower fees). Market orders = taker.
- Practical: Set order expiration to "scheduled event start" to avoid getting filled at stale lines during live.
- Look for low liquidity markets (<$5,000 at price) = likely stale line. Large gap between ask levels (93c then 96c) = stale.
- Devig to Pinnacle/Circa. Always account for taker fee in calculations.

**"Kalshi Automated Strategy Explained (Live Demo)" — Process Over Profit**
- URL: https://www.youtube.com/watch?v=eq8xoz7o07U
- 15-minute crypto Up/Down market bot. "High Probability Farmer."
- Trigger at 85 cents (85% implied probability). One trade per 15-min session. ~$1/trade average.
- Simple but illustrative: higher trigger = lower risk but fewer opportunities. Lower trigger = more exposure, higher target returns.
- Risk: 15% of events wipe out original investment. Takes time to recover from losses.

### AI / Bot Videos

**"How AI Made $150,000 on Polymarket Without a Single Human Trade"**
- URL: https://www.youtube.com/watch?v=Oc7pziORRjg
- Bot executed ~9,000 trades exploiting pricing inefficiencies during trading window transitions. 1.5-3% per trade. ~$150,000 total.
- Two strategies: (1) Pre-filling bids before sentiment shifts. (2) Time-sensitive bidding at market window switches.
- Constraints: Liquidity $5,000-$15,000 per side. Low four figures per trade max. Larger desks can't play without moving market.
- AI removes guesswork: monitors liquidity, flags low-risk entries, simulates scenarios.

**"How Someone Turned $50 into $435,000 on Polymarket"**
- URL: https://www.youtube.com/watch?v=jL8cuzkclHg (no subtitles available)
- Reverse-engineered latency arb strategy. Rust bot. 0.3-0.8% per trade. ~$400-700/day potential.
- Exploits price lags between real-time BTC feeds and Polymarket contract pricing.
- Warning: edges closing in 2026 due to dynamic fees and patches.

**"Inside Kalshi's Market Making Engine"**
- URL: https://www.youtube.com/watch?v=XmflYeh8OKM
- Two groups of markets: long-tail (hard to price, incentivized MMs) vs classic crypto/sports (clear demand, easier to price).
- Sports MM: fee rebates with hard conditions (uptime, spreads, top-of-book size). Goal is book stability.
- Live games: want book to go wider during critical moments but stay open. Don't want zero liquidity when touchdown imminent.

**"Kalshi Taker vs. Maker Explained" — TC Trading**
- URL: https://www.youtube.com/watch?v=5j4-R8PwYl4
- Taker fee: `0.07 * contracts * price * (1 - price)`, rounded up to nearest cent.
- Maker fee: 4x less (`0.0175` multiplier). Same formula.
- Key: Use limit orders to pay 4x less fees. Critical for high-frequency strategies.

---

## 9. In-Play Tennis Trading — Practical Edge Patterns

### Break Point Dynamics
- Break point saved = small price move. Break point converted = large price move (60-120 ticks in 8 seconds on Betfair).
- Markets OVER-REACT to breaks and UNDER-REACT to break-backs.
- Recreational money chases momentum (piles in on breaker). Reacts slowly when opponent breaks back.

### Strategy: Back Favourite at 0-40 Down
- Favourite serving, falls 0-40. Price spikes from ~1.50 to ~1.85.
- Back favourite at elevated price. If hold (most do), price recovers to 1.45-1.50 within 1-2 games.
- Win: ~+12-14% of stake. Loss (broken): ~-25-30% of stake.
- ATP players hold from 0-40 roughly 15-20% of the time (varies by surface/player).

### Strategy: Lay the Breaker (Fade the Spike)
- After break of serve, breaking player's price drops 60-120 ticks in seconds.
- Wait 60-90s for liquidity to settle. Lay the breaker.
- Breaker must hold next service game (75-82% ATP hold rate). If broken back, price reverts to pre-break level.

### Tiebreak Trading
- Highest volatility window. Leader price can swing 1.40 -> 1.85 -> 1.20 -> 2.10 over 12-18 minutes.
- Tiebreaks favour better server. Mini-breaks (winning on opponent's serve) are mean-reverting.
- Edge: back the player who lost first mini-break (price over-reaction).

### Latency Hierarchy
1. Tournament data feeds (Sportradar, Stats Perform): 0.5-1.5s delay
2. Courtsiders (people at court with phone): 2-5s delay
3. TV/stream: 10-20s delay
4. Public APIs: 5-15s delay
- **Our position**: API-Tennis WebSocket is 5-15s. Better than TV but worse than premium feeds. Edge comes from model quality, not raw latency.

---

## 10. Key Takeaways for Ghost Trader Strategy

### What Works
1. **Point-based probability models**: Serve-win probability -> Markov chain -> exact match probability. TENNISPROB approach (Magnus & Klaassen 2003).
2. **Score-state-dependent serve probabilities**: Not iid. Break points matter. DTMC model (2019 paper).
3. **Bayesian hierarchical player skills**: Ingram (2018) model for pre-match baseline. Surface-specific, time-varying.
4. **Avellaneda-Stoikov for market making**: Adapted for binary options. Inventory skew is critical (sign must be correct!).
5. **Monopoly regime detection**: When you're the only liquidity provider near extreme prices, size aggressively. 60% of edge in Paradigm challenge.
6. **Cross-market EV**: Kalshi lags sharp sportsbooks in live setting. Devig Pinnacle/Circa lines for true fair value.
7. **Break point over-reaction**: Markets over-react to breaks, under-react to break-backs. Trade the mean-reversion.

### What Kills You
1. **Inverted inventory skew**: One minus sign = trend-following instead of mean-reverting. $150 in 20 minutes (Rodney L.).
2. **Wrong sigma**: Binary options need sigma 0.05-0.15, not 0.001. Razor-thin spreads on everything = death.
3. **No category allowlist**: Ghost service trades tennis when it shouldn't. $35 lost (MAXIMUS).
4. **Fee death zone**: Below $0.15, fee drag makes consistent profit mathematically near-impossible.
5. **Stale data contamination**: Mix buggy-era wins with clean data = fake 82% win rate, real is 57% (Pierno).
6. **Fat tails**: Gaussian assumption for weather = 2-sigma events happen 2x as often as predicted (MAXIMUS weather).
7. **Exit liquidity**: If you're slower than other bots, you're providing exit liquidity for them. You confirm what faster traders already knew.
8. **No stop criteria**: 369 trades with zero wins because no rule fired to pause (BotForKalshi).
9. **MVE/combo markets**: Untradeable positions. Hard filter on `KXMVE*` tickers required.
10. **Too many markets**: 50 markets with 20-contract cap = fractional position scattered everywhere. Too thin to earn spread.

### Structural Advantages We Have
- Real-time WebSocket data from Kalshi (not polling REST).
- Real-time score feed (API-Tennis WebSocket) for fair value computation.
- SQLite historical data for backtesting strategies.
- Go performance for low-latency order management.
- Match-point detection algorithm already implemented.

### Structural Disadvantages
- Not a market maker (no maker fee rebates currently).
- Data latency 5-15s vs premium feeds at 0.5-1.5s.
- Small bankroll = position size constraints.
- Tennis markets on Kalshi are illiquid with wide spreads (this is both risk and opportunity).

---

## 11. Open Questions

1. **Can we beat Elo with a point-based model?** Kovalchik (2016) says no. Ingram (2018) says maybe. Our advantage: we have real-time point data, not just pre-match stats.
2. **Is the break-point over-reaction pattern present on Kalshi?** Documented on Betfair. Kalshi has different participant mix (more retail, less sharp). Could be stronger or weaker.
3. **What is the right sigma for Kalshi tennis markets?** Binary options 0-1 range. Need empirical measurement from our tick data.
4. **Can we use the monopoly regime insight?** When Kalshi tennis markets have one-sided orderbooks near match end, we could be sole liquidity provider.
5. **Should we market make or take?** Maker fees 4x lower. But market making requires inventory management and constant quoting. Taking requires clear directional edge.
6. **Cross-platform arb with Polymarket?** They have tennis markets too. 2.7s average arb window. Need sub-second execution.
