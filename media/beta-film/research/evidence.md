# Evidence base for the Dorylinae closed-beta film

Compiled 8 Oct 2026 from web research (WebSearch / WebFetch). Nothing in the repo was touched.
Purpose: let the creative choices (length, structure, text versus voice, formats, call to action) rest on data.
This file is about **how to communicate**. It makes no product claims. Product claims stay governed by
`product-facts.md` and `beta-facts.md` in this folder.

## How to read the source tags and confidence

| Tag | Meaning |
|---|---|
| **[P]** | Primary platform documentation or a platform's own help page, read directly |
| **[R]** | Peer-reviewed paper or conference paper (read directly or abstract only, said per item) |
| **[D]** | First-party dataset report from a vendor or platform (Wistia, Vidyard, LinkedIn, Reddit, Google), read directly |
| **[S]** | Secondary: a trade-press or blog summary of a first-party study. The original could not be opened. Numbers are as reported there |
| **[B]** | Practitioner or vendor blog. Useful for direction, weak as proof |
| **[A]** | Single-case anecdote |

Confidence per finding: **High** = primary doc, or large peer-reviewed or first-party dataset, consistent across sources.
**Medium** = decent source but old, lab-only, vendor-run, or only secondhand. **Low** = blog-level, unsourced, or conflicting.

Honest limit of the whole file: **no study I found tests a recruitment film for a closed beta of an unknown developer tool.**
The evidence comes from neighbouring settings (platform ad data, B2B video benchmarks, multimedia-learning research,
developer-marketing writing). The "Implications" section marks which recommendations are inference.

---

## Summary table (decision, evidence, confidence)

| Decision | What the evidence says | Confidence |
|---|---|---|
| Length | Videos under 1 minute hold the highest share of viewers (about 50 to 66 percent watched or finished). Little difference between 1 and 5 minutes on Wistia; drop-off steepens past 5 minutes. Feed platforms recommend 15 to 30 seconds for ads (LinkedIn, Reddit) and 15 or fewer on X. Roughly half of viewers are gone by the midpoint, so what must land has to land in the first half. | High on shape, Medium on exact numbers |
| Sound off | Most feed video is watched muted (reported 69 to 85 percent depending on source and year), but YouTube is mostly sound-on. Burned-in type is mandatory for feeds. | High that it is a majority in feeds, Medium on the figure |
| Hook | Recall starts at fractions of a second; mobile feed posts get about 1.7 s of looking on average; weak ads lose 50 to 70 percent between seconds 5 and 7. Show brand and subject by about second 3 to 5. | Medium |
| Explain versus list | For radically new products, abstract benefit messaging beats attribute lists (peer-reviewed, 3 studies). Showing the product in use beats showing how it looks. Step-by-step (process) demos beat result-only demos. | Medium (lab studies) |
| Call to action | One destination. Mid-video CTAs convert better than end-only. Real scarcity helps most for unfamiliar brands, but fake urgency is a known dark pattern and developers punish it. | Medium |
| Voice | People state a preference for human voices; measured ad performance is often equal, but a viewer who suspects AI trusts less. The developer audience is AI-saturated and sceptical. | Medium-Low |
| Formats | 16:9 for YouTube, X, web; 4:5 is LinkedIn's recommended vertical ratio and Reddit's 2026 mobile recommendation. 30 fps, H.264, AAC. | High (specs) |

---

## Q1. Optimal length per platform, and how retention drops

### 1.1 Retention by length (numbers)

| Source | Data | Finding | Tag | Conf. |
|---|---|---|---|---|
| Wistia, *2022 State of Video Report* (2021 data; 13M+ videos, 79M hours of viewing; survey of 900+ professionals). https://wistia.com/about/state-of-video-2022 | Average engagement (share of the video watched) | Under 1 min 50%; 1 to 3 min 48%; 3 to 5 min 49%; 5 to 30 min 38%; 30 to 60 min 25%; over 60 min 15%. Page text says only "The shorter the video, the higher the engagement rate" and "almost every format performs best under 5 minutes". The per-bucket numbers sit in a chart image and reached me through a search summary of that chart. | [D]/[S] | Medium |
| Wistia blog, *How to Choose the Right Marketing Video Length for Any Goal* (July 2025, citing the 2026 State of Video Report, 13M+ videos). https://wistia.com/blog/optimal-video-length | Average engagement | Under 1 min: 52% (product videos 50%, testimonials 46%). 1 to 5 min: viewers typically watch over 50% of educational and tutorial videos. Engagement declines after 5 min. "Most viewers drop off around the halfway point", so "put the most important information in the first half". Brand awareness and social: under 1 min. Education and product demos: 1 to 5 min. | [D] | Medium-High |
| Wistia 2024 report coverage. https://wistia.com/blog/insights-state-of-video-report | Engagement | Little difference between 1 and 5 minutes; sharp drop beyond 5. Instructional videos reach 74% at 3 to 5 min versus 43% for all videos that length. | [D]/[S] | Medium |
| Vidyard, *Video in Business Benchmark* (2023 edition, 2022 data, about 1.78M customer videos), via MarketingProfs and Streaming Learning Center summaries. Original not opened (blocked). https://www.marketingprofs.com/charts/2023/49949/business-video-benchmarks-retention-rates-by-length | Finish rate | Under 1 min 66%; 1 to 2 min 56%; 2 to 10 min 50%; 10 to 20 min 39%; over 20 min 22%. Overall 53%. The earlier edition (2021 data): under 1 min 62%, over 20 min 26%, and "viewers were most attentive during the first 10% of a video, whatever its length". Demo and explainer were the most common formats. | [S] | Medium |
| Guo, Kim and Rubin, *How video production affects student engagement*, ACM Learning@Scale 2014 (6.9M watching sessions, 4 edX courses). PDF read. https://www.cs.rochester.edu/hci/pubs/pdfs/edX-MOOC-video-production-and-engagement_LAS-2014.pdf | Median engagement time | "Video length was by far the most significant indicator of engagement." Median engagement time is "at most 6 minutes, regardless of total video length". Students "often make it less than halfway through videos longer than 9 minutes". Informal talking-head video outperformed slides alone and studio polish; fast-speaking (high-energy) instructors held people longer. Educational context, so transfer with care. | [R] | High for the shape, Low for transfer |
| Facebook (2016), reported by Marketing Dive / Social Media Today | Retention after 3 s | Of people who watch the first 3 seconds of a Facebook video, 65% reach 10 s and 45% reach 30 s. | [S], old | Low-Medium |
| Google ABCD guidance, via ppc.land summary of Think with Google. https://ppc.land/mastering-youtube-advertising-with-the-abcd-framework/ | Early loss | Weak creative can lose 50 to 70% of the target audience between second 5 and second 7. Top ads get most of their core proposition into the first 5 seconds. (Original Think with Google page now redirects; PDF unreadable.) | [S] | Medium |
| Reddit, 2022 data via eMarketer, and Reddit *2026 Creative Best Practices* (nearly 150,000 in-feed ads, 7,000 advertisers) via ppc.land. https://ppc.land/reddits-2026-ad-creative-study-what-actually-moves-conversion-rates/ | Reddit video | 88% of users who watch a Reddit video watch the first 6 seconds (2022). In the 2026 study, ads under 6 s had the highest conversion impact, then 6 to 10 s, then 10 to 15 s. Lower-funnel focus (conversions), not completion. | [S] | Medium |
| B2B Institute with MediaScience, 2023 (109 real B2B ads, 770 LinkedIn users, eye tracking), via Marketing Week / Net Influencer summaries. https://www.marketingweek.com/b2b-video-ads-fail-attention/ | Attention in LinkedIn feed | Ads were on screen 12.3 s on average; viewers paid attention 3.7 s. Only about 19% both remembered the ad and attributed it to the right brand. | [S] | Medium |
| LinkedIn Creative Labs, *Art and Science of Video Storytelling* (July 2025; 13,000+ B2B video ads, 550,000 frames), via Net Influencer. https://www.netinfluencer.com/?p=36939 | Engagement by length | "Cinematic brand films of 31 to 60 seconds delivered a 61% engagement uplift over six-second formats." | [S] | Medium |

Reading across them: engagement is highest in the sub-minute bracket and flat-ish to 5 minutes, then falls. Inside any video,
the biggest loss is early (first 5 to 10 seconds, then first 10 percent), and the midpoint is where roughly half have left.
A 60-second film should expect to be watched to the end by roughly half of the people who start it
(Wistia 50 to 52% average watched, Vidyard 66% finish for under a minute).

### 1.2 Length guidance and limits per platform

| Platform | Guidance | Hard limits | Source | Tag | Conf. |
|---|---|---|---|---|---|
| **X** | Ads: "15 seconds or less" recommended to maximise branding; short copy (50 to 100 characters) performs best; keep one message and one destination | Organic: 140 s for non-Premium; Premium up to 4 h (1080p, web and iOS), 10 min on Android; file 512 MB (16 GB Premium). Ads: up to 2:20 | business.x.com video views and creative best practices (read through search summaries because direct fetch returned 402); help.x.com *About longer videos for X Premium* (via search summary; direct fetch 403) | [P]/[S] | Medium |
| **LinkedIn** | Ads: "15 to 30 seconds" recommended to qualify for all Feed and Audience Network placements; in-stream up to 90 s; clips under 30 s loop until 30 s of playback | Ads: 3 s to 30 min. Organic native: commonly 10 min mobile, 15 min desktop, 5 GB (third-party, conflicting) | https://www.linkedin.com/help/lms/answer/85306 (read in full) | [P] | High (ads), Low-Medium (organic limits) |
| **YouTube** | Intro metric is "percentage of audience still watching after the first 30 seconds"; "audience size typically shrinks over the video's length", so move compelling content earlier; no recommended length | Shorts: up to 3 minutes and square or vertical (a 16:9 upload is not a Short) | https://support.google.com/youtube/answer/9314415 and https://support.google.com/youtube/answer/15424877 (both read) | [P] | High |
| **Reddit** | Ads: 5 to 30 s recommended; 2026 study says under 6 s converts best, vertical 4:5 preferred on mobile | Ads: min 5 s, max 15 min, 1 GB. Organic: about 15 min, 1 GB (third-party) | Agency spec sheet reproducing Reddit's *Ad Unit Specifications* (March 2026): https://mediamaxnetwork.com/wp-content/uploads/2026/03/Reddit-Specs.pdf ; Reddit's own help page would not load | [S] | Medium |
| **Website hero** | Wistia: homepages, video galleries and product pages have the highest play rates, and viewers typically get about halfway. For silent background loops, practitioners cap at about 10 s and under 4 to 5 MB; MDN advises removing the audio track from silent clips | n/a | https://wistia.com/about/state-of-video-2022 ; web.dev/learn/performance/video-performance ; MDN *Multimedia: video* | [D]/[B] | Medium |

**Confidence for Q1 overall: Medium-High.** The shape is solid and repeated by independent sources. The exact per-length percentages are vendor
benchmarks, partly secondhand.

---

## Q2. Sound-off viewing

| Source | Finding | Tag | Conf. |
|---|---|---|---|
| Digiday, 17 May 2016. https://digiday.com/media/silent-world-facebook-video/ (read) | "As much as 85 percent of video views happen with the sound off." LittleThings and Mic each report 85% of Facebook views silent; PopSugar 50 to 80%; MEC North America reports 85 to 90% silent for branded video, and found sound-on versus sound-off made no difference to brand lift or purchase intent. | [D] publisher self-report | Medium (old) |
| Verizon Media with Publicis Media survey, April 2019, 5,616 US adults 18 to 54, via Streaming Media. https://www.streamingmedia.com/Articles/News/Online-Video-News/80-of-Video-Caption-Users-Arent-Hearing-Impaired-Finds-Verizon-131860.aspx (read) | 69% watch with sound off in public; 25% watch with no sound even in private; 80% of caption users are not hearing impaired; 80% are more likely to watch a whole video when captions are available. In ad tests on mobile, captions gave +8% ad recall and +10% memory quality. **Pairing a brand logo with captions cut memory by 12% for highly emotional and visual ads.** | [S] | Medium |
| Google internal data 2018, via ppc.land. | 95% of YouTube videos are viewed **with sound on**. Platform context matters: YouTube is the exception. | [S] | Low-Medium |
| Meta, various, via Meta Marketing Summit recap and ppc.land | Reels: 75 to 90% watched with sound on (sources vary: 75, 80, 90). Instagram Stories: 60% sound on (old). | [S] | Low |
| LinkedIn | The oft-repeated "79 to 85% of LinkedIn video is watched muted" has no traceable primary source; LinkedIn's own spec page says an unmuted desktop feed video keeps playing, implying muted autoplay by default. | [B] | Low (number), Medium (direction) |
| Reddit 2026 Creative Best Practices, via ppc.land | Video with overlay text, which Reddit counts to include closed captions, had **8.2% higher average conversion rate** than video without. | [S] | Medium |
| LinkedIn Creative Labs 2025, via Net Influencer | Bulleted captions raised dwell time by 30%. | [S] | Medium-Low |
| X creative best practices (search summary) | "Include captions or another sound-off strategy"; X says 97% of its audience attends to imagery. | [S] | Medium |
| LinkedIn spec page (read) | Uploaded captions must be an SRT with text only: "Custom formatting such as font colors isn't supported." So a branded caption look has to be burned into the picture. | [P] | High |

**Implications.** In the feeds where we will post (X, LinkedIn, Reddit), most first impressions are silent, so every claim must be
readable as type. YouTube viewers are mostly sound-on, so sound design is worth doing there, but sound must never carry a claim.
Burned-in text is also how to keep brand typography, since SRT cannot be styled. Watch out for the one negative result:
logo plus captions reduced memory for emotional and visual ads (Verizon/Publicis, 2019); keep the wordmark clear of the caption zone.

**Confidence for Q2: Medium-High** (direction is consistent; numbers are old and mostly vendor or publisher-reported).

---

## Q3. The hook

### 3.1 How many seconds viewers decide

| Source | Finding | Tag | Conf. |
|---|---|---|---|
| Facebook IQ commentary, 2016, via Marketing Dive / ITWeb | Mobile News Feed posts get **1.7 seconds** of looking on average (2.5 s desktop). Independent Fors Marsh work: feed content is recalled at a statistically significant rate after **0.25 s**. A Nielsen study with Facebook and Twitter: significant mobile video ad recall at **1 s**. Smartphone users scroll 41% faster than desktop users. | [S] | Medium (old) |
| Google ABCD | Open with impact; **at least 2 shots in the first 5 seconds**; introduce the product or brand in the first 5 s; people and faces attract early attention; weak creative loses 50 to 70% between seconds 5 and 7. Campaigns following the guidelines averaged +30% short-term sales likelihood and +17% long-term brand contribution (Google/Kantar 2021). Based on ads analysed with Ipsos (5,000), Nielsen Neuro (6,000) and Kantar (15,000+). | [S] | Medium |
| Reddit | 2022: 80% of top-performing Reddit video campaigns show the brand or logo within the first 3 s. 2026: a logo in the **first or last 3 seconds** had the best conversion-lift impact; a mid-video logo or none at the start and end did worse. | [S] | Medium |
| B2B Institute / MediaScience 2023 | In LinkedIn feed, ads with **3 or more brand mentions** were correctly attributed 48% of the time versus 32% with one; "no such thing as over-branding"; "the brand must always be the star". | [S] | Medium |
| Vidmob study of LinkedIn (16.7K ads, 804M impressions, 2021 to 2022), via Search Engine Land | Showing messaging in the opening quarter of a video was tied to about +149% in 25% view-through; a logo in the first 2 s about +17% click-through. Vendor-run, 4 years old. | [S] | Low-Medium |
| Meta and Nielsen, via vendor blogs | "People who watched under three seconds created up to 47% of campaign value." Could not trace to an original. | [B] | Low |
| Verizon/Publicis 2019 (above) | Logo plus captions reduced memory for emotional ads. | [S] | Medium |

Conclusion: the decision to stop happens in the first 1 to 3 seconds; the decision to stay happens by about second 5 to 7; the audience
is half gone at the midpoint. Brand and subject identification should be visible by second 3 to 5.

### 3.2 Opening patterns for an unknown product

Direct evidence for any one pattern is thin. What converges:

1. **Name the thing early and often.** Brand in the first 3 to 5 s (Google, Reddit); repeated brand mentions beat single ones (B2B Institute).
2. **Change and motion in the first seconds**, at least two shots in the first 5 s (Google).
3. **Show a real, recognisable situation or a person** (Google faces; LinkedIn Creative Labs 2025 reports real emotion +78% engagement and named experts speaking conversationally up to +53%, but those numbers come from third-party copies of the report, Low-Medium).
4. **Put the message in the first quarter** (Vidmob, Low-Medium).
5. **Avoid the generic B2B opening.** System1 and the B2B Institute found about 75% of B2B ads score at the bottom of a 1 to 5 effectiveness scale. Common traits of the weak ones: voice-over, stock imagery, block text, logo only at the very end, no story arc (via Marketing Week). Developers say the same in their own words: "I shouldn't need a translator to figure out what your product does"; brand-first storytelling feels like "a Netflix trailer for an API" (Mighty & True, qualitative, Low).
6. **A teaser works only for appealing, new things, and only with a payoff.** Journal of Marketing research (AMA release, 2020) found showing about two-thirds of an appealing item raised preference (and a Facebook teaser field test reproduced it), but "this effect is limited to visually appealing items" and does not replicate for unattractive stimuli. A ClickZ column warns every teaser needs a ready payoff. A 2014 thesis found revealing the product category in a teaser reduced curiosity. For a recruitment film the payoff is the product, so hold curiosity to the first seconds only.

**Confidence for Q3: Medium.**

---

## Q4. Explain versus feature list; real UI; explainer versus teaser; concrete versus abstract

### 4.1 Findings

| Source | Finding | Tag | Conf. |
|---|---|---|---|
| Lee, Chu and Roy (2024), *Journal of Business Research* 182, "Enhancing consumer evaluation of new products: the role of innovation newness and communication strategy". https://ideas.repec.org/a/eee/jbrese/v182y2024ics0148296324002704.html (abstract read) | Three studies. Greater newness increases psychological distance. **Attribute (concrete) appeals work better for incrementally new products; benefit (abstract) appeals work better for radically new products**, mediated by processing fluency. The matching holds for utilitarian products. | [R] | Medium (lab, abstract only) |
| Frontiers in Psychology (2022), product-presentation videos. https://www.frontiersin.org/journals/psychology/articles/10.3389/fpsyg.2022.812579/full (read) | Three pre-experiments and two formal experiments (n = 144 and 188). **Product-usage videos raised purchase intention more than appearance videos**, mediated by perceived diagnosticity and mental imagery. The gap disappeared when product ratings were low. | [R] | Medium |
| Ringler, Sirianni, Peck and Gustafsson (2024), *Journal of the Academy of Marketing Science* 52(2), "Does your demonstration tell the whole story?" (abstract and coverage read). https://ideas.repec.org/a/spr/joamsc/v52y2024i2d10.1007_s11747-023-00934-6.html | Five studies across online video, in-store demos and QVC-style broadcast: **process demonstrations (step by step) beat outcome-only demonstrations** on purchase intention, via absorption in the story ("cognitive flow"). Effect weakens when the viewer feels other people watching. | [R] | Medium |
| Kumar and Tan (2015), cited in the Frontiers paper | Sales of apparel rose after online retailers added product presentation videos (original not opened). | [R]/[S] | Low-Medium |
| Wistia 2022 survey (900+ professionals) | Product videos and webinars rated the most impactful video types; homepage and product pages have the highest play rates. | [D] | Medium |
| Wyzowl *State of Video Marketing 2026* via SocialPilot and others (small sample, about 266 marketers) | 68% of video marketers make explainers; 39% make product demos. This measures what teams make, not what works. 63% of consumers would rather watch a short video than read to learn about a product. | [S] | Low |
| Dropbox, TechCrunch founder interview (2011). https://techcrunch.com/2011/11/01/founder-storie-how-dropbox-got-its-first-10-million-users/ (read) | A screencast aimed at Digg readers, shown during Dropbox's private beta, brought **75,000 waiting-list sign-ups in 24 hours against an expected 15,000**; the team added "easter eggs aimed at the Digg audience". The popular "5,000 to 75,000" figure comes from a different, earlier video and is repeated inconsistently, so quote the TechCrunch version only. Closest analogue to our case (unknown product, technical audience, beta waiting list, demo video), but one anecdote. | [A] | Low |
| Zakary Tormala contrast experiment, company blog (sample not reported, unverified) | Listing problems with the current thing next to the new thing's features gave a 14%+ purchase-intent lift over a future-only message. | [B] | Low |
| Mighty & True (Jan 2026, updated Jul 2026): qualitative review of 100+ public developer threads on Reddit, Hacker News, Discord and GitHub, analysed with ChatGPT | Developers dislike: unclear homepages ("If I have to click four pages in to find out what it does, I won't"), missing technical detail ("No architecture diagram? No thanks"), over-promising. They like clear direct copy, docs, transparency about where the product doesn't fit. | [B] | Low |

### 4.2 Reading

- **Unknown product: lead with the plain-language "what it is", then prove it with a concrete, in-use, step-by-step demonstration.** The newness research says abstract benefits suit radically new products; the demo research says use beats looks and process beats result. These do not conflict: benefit first (fluency), demonstration second (diagnosticity).
- **A feature list is the weakest of the three.** No study I found shows a feature list beating a problem or benefit framing for an unfamiliar product. Attribute lists work for incremental products people already understand.
- **Real UI helps.** Both video studies favour showing the product in use. Developer commentary (low quality) says the same: show real terminal output and diagrams, not abstractions.
- **Explainer beats teaser for recruitment.** Wistia matches under-1-minute promos and teasers to "brand awareness" and 1 to 5 minute demos to "education". A recruitment film needs both: a short explanation plus a proof.

**Confidence for Q4: Medium** (consistent direction; lab studies on consumer goods; nothing on developer tools).

---

## Q5. Calls to action, scarcity, and recruiting developers

### 5.1 One CTA versus many

| Source | Finding | Tag | Conf. |
|---|---|---|---|
| X creative best practices (search summary; direct fetch 402) | "Provide only one exit point"; direct prompt in the headline; no @mentions; hashtags and multiple emojis prohibited in ads. | [S] | Medium |
| Unbounce analysis of 18,639 landing pages (2018 to 2019 coverage) | Pages with one CTA averaged 13.5% conversion, two CTAs 11.9%, three CTAs 10.5% (a second write-up says five or more). Correlational, uncontrolled. | [S] | Low-Medium |
| Scheibehenne, Greifeneder and Todd (2010), *Journal of Consumer Research* meta-analysis of choice overload | Mean effect "virtually zero" with large variance; no sufficient conditions identified. So "fewer choices always converts better" is not a law. A single CTA is justified as a low-cost clarity measure, not as a proven lift. | [R] | High (on the null) |
| Wistia, CTA analysis of 36,000+ CTAs in 2024 (https://wistia.com/learn/marketing/using-video-ctas , read) and an earlier summary | Average video CTA conversion about 16%. Placement: mid-roll 22.1%, post-roll 16.9%, pre-roll 8.9%, but 92.5% of users put the CTA at the end and only 7.3% mid-roll, so selection effects apply. Wistia's recommended CTA position for videos **under one minute is within the first quarter**. Action verbs (Click, Download, Register, Sign up, Buy, Try) recommended. | [D] | Medium |
| Stack Overflow Developer Survey insights for B2B marketers (year not stated on page). https://stackoverflow.co/advertising/resources/stack-overflow-developer-survey-for-b2b-tech-marketers/insight-1/ (read) | How developers evaluate new tools: **free trial 75%**, ask technologists they know or work with almost 73%, visit communities like Stack Overflow 61%. | [D] | Medium |
| Hacker News *Show HN* guidelines. https://news.ycombinator.com/showhn.html (read) | Off topic: "blog posts, sign-up pages, newsletters, lists"; "Don't post landing pages or fundraisers." "Please make it easy for users to try your thing out, ideally without barriers such as signups or emails." "Please don't ask friends to upvote or comment." | [P] | High |

### 5.2 Scarcity and exclusivity

| Source | Finding | Tag | Conf. |
|---|---|---|---|
| Barton, Zlatevska and Oppewal (2022), *Journal of Retailing* 98(4), meta-analysis, 416 effect sizes from 131 studies. PDF read. https://doi.org/10.1016/j.jretai.2022.06.003 | Scarcity raises purchase intention: **δ = 0.31** overall. Stronger for **low-familiarity brands (δ = 0.41) than high-familiarity (δ = 0.21)**, so about twice as large for unknown brands. Supply-based scarcity (limited quantity) works best for experiences; time-based for high-involvement; demand-based for utilitarian. Caveat from the authors: only 24 of 416 effect sizes measured actual behaviour and only 6 came from field studies; the rest are hypothetical. Time-based scarcity is mostly tied to promotions whose deadlines get extended. | [R] | High on direction, Medium on size |
| Mathur et al. (2019), *Dark Patterns at Scale*, ACM CSCW (arXiv 1907.07032) | Crawl of about 11K shopping sites found 1,818 dark-pattern instances; countdown timers and limited-time messages on 437 sites, some fake (timers that reset, "ends soon" with no date). | [R] | High |
| Superhuman invite-only waitlist, First Round Review and secondary summaries | The waitlist (reported from 70,000 to 250,000 people) was **capacity-driven**: the team could onboard only a fixed number of people per week by hand and Rahul Vohra said it was not a scarcity tactic. Invitees could bring teammates. | [A]/[S] | Low |
| Gmail invite-only launch, retrospective commentary (Cornell and others) | Invite scarcity built hype; commentary is retrospective, no controlled comparison. | [B] | Low |
| Georgetown/Management Science and Frontiers (2022) on queues | Longer waits raise consumption and curiosity; shown for hedonic goods. | [R] | Low-Medium for our case |
| LMU Munich (2026) | Scarcity signals can undermine peer effects; exclusivity-seekers may avoid products that look too popular. | [R] | Low |

Reading: scarcity helps, and helps more for brands nobody knows, but only the real kind is safe with this audience. The real constraints here
(invite-only, a limited first wave, codes that expire) are facts in the owner-approved spec, not tactics. State them as facts, with no
countdown, no "hurry", no "only X left" theatre. A Show HN will not take a recruitment page, so Hacker News is not a channel for the
film; if posted at all it is a normal discussion post, and the community explicitly dislikes sign-up gates.

### 5.3 What developers dislike, and how to phrase recruitment

| Source | Finding | Tag | Conf. |
|---|---|---|---|
| Stack Overflow Developer Survey 2025, AI section (about 49,000 respondents). https://survey.stackoverflow.co/2025/ai (read) | 84% use or plan to use AI tools; favourable sentiment fell from 70%+ (2023, 2024) to about 60%; **46% distrust the accuracy of AI tools versus 33% who trust it**; only 3.1% highly trust the output; about 31% use AI agents at least occasionally, 37.9% do not plan to. | [D] | High |
| Mighty & True (above) | See 4.1. Dislikes: buzzwords, unclear homepage, over-promising, missing technical detail, tone-deaf brand storytelling. Likes: clear copy, docs, transparency about limits, product-led onboarding, real-user validation. | [B] | Low |
| Redis blog "The marketing buzzwords developers hate" and daily.dev business pages | Quote an untraceable "survey of 800 developers"; daily.dev lists 72% ad-blocker use and other figures with no source. I do not rely on these numbers. The qualitative claim (vague buzzwords repel) agrees with Mighty & True. | [B] | Low |
| Stack Overflow's own ad guidance (search summary) | Avoids animated and video ad formats because developers "see these ads as a distraction from the problem they're actively working to solve". | [S] | Low-Medium |
| DevRel newsletter counterpoint | Developers resist clumsy outreach, not marketing as such. | [B] | Low |
| Hacker News guidelines | See above: no sign-ups as a Show HN. | [P] | High |
| No published A/B test | I could not find a controlled test comparing "apply", "request access" and "join waitlist" for developer-tool betas. Label should match the real mechanism (a reviewed request implies "request access" or "apply"; a queue implies "waitlist"). | n/a | n/a |

**Phrasing implications (inference).** Say who it is for in one line (two to five people, each with their own coding agent); say what is asked
of them (install one binary, use it instead of chat for "can you look at this", give feedback) so the wrong people self-select out;
say what it does not do yet (the repo already has a known-limitations doc); say it is free only if the owner confirms; avoid hype words
(this also matches the brand voice rules: no "revolutionary", "seamless", "supercharge", "unleash").

**Confidence for Q5: Medium** (scarcity: strong meta-analysis but mostly hypothetical choices; developer-dislike evidence is weak but consistent).

---

## Q6. Voice-over, text-only, and synthetic voices

### 6.1 Multimedia-learning research (an explainer is a teaching task)

| Principle | Finding | Source | Tag | Conf. |
|---|---|---|---|---|
| Voice | People learn better from a human voice than a machine voice: median d about 0.74 to 0.78 across a handful of experiments (Mayer's own tallies, not a pooled meta-analysis). Mayer, Sobko and Mautone (2003), *J. Educational Psychology* 95. | Search summaries of Mayer's handbook chapters | [R] | Medium |
| Voice, modern TTS | Mixed. Craig and Schroeder (2019, *J. Educational Computing Research* 57(6)) and a 2022 follow-up report no significant difference between modern neural TTS and a human voice in some designs; Mayer's group still warns about monotone machine voices. Could not read the 2019 abstract. | Search summaries | [R]/[B] | Low-Medium |
| Modality | Narration with graphics beats on-screen text with graphics: median d = 0.76 (53 of 61 tests, Mayer 2014); 0.65 in a 76-study review; a 91-study independent meta-analysis (Wang, Xie, Li, 2016) found much smaller effects (retention d = 0.24, transfer d = 0.25). | Search summaries | [R] | Medium |
| Redundancy | Narration plus identical on-screen text hurts learning (16 of 16 tests, median 0.86 in Mayer 2014), but the effect reverses or vanishes for low prior knowledge, system-paced material, **picture-free material, and non-native listeners**; key-term text is better than verbatim text. | Adesope and Nesbit (2012) and others, via search | [R] | Medium |
| Pace and style | In edX data, informal, higher-energy delivery held people longer than polished studio lectures (Guo et al. 2014). | read | [R] | Medium |

Reading: if narration is used, the picture should carry the demonstration and the words should add, not duplicate. But the key
condition here is that most feed viewers will not hear any narration, so the "words in the picture" route is forced anyway.

### 6.2 Perception of synthetic (TTS) voices in advertising

| Study | Sample and method | Findings | Tag | Conf. |
|---|---|---|---|---|
| WPP Media with Choreograph NeuroLab and MediaScience (27 Oct 2025). https://www.wppmedia.com/news/ai-voices-audio-ads (read) | 55 participants, labs in Chicago, New York, Austin, neuro and behavioural measures | People rated ads higher when they believed the voice was human, but **AI-voiced ads matched human-voiced ads on brand engagement, purchase intent and neural attention**. Only 42% (sentences) and 47% (full ads) correctly identified generic AI voices. | [D] | Medium (small n, agency-run) |
| Studio Resonate (SiriusXM Media) with CloudArmy, *The Power of the Human Voice in an Age of AI* (11 Aug 2025). https://www.contentgrip.com/studio-resonate-ai-voice-study/ (read) | 1,300 US respondents, invented brands, implicit testing | Human voices: **+24% brand attraction, +23% trust, +25% positivity** versus matched AI voices. Telling listeners they would hear AI voices cut trust by 27% **even when the voices were human**. People claimed 60% confidence in telling them apart but scored at chance (50%). | [D] | Medium (vendor with a stake in human voices) |
| Azerion with Differentology (white paper, 14 Jul 2026), via ExchangeWire / PPC Land | 3,000 UK respondents, two retail brands | Only 29% identified the AI version as AI; 37% thought it was human; 26% of those who heard the human recording thought it was AI. Regionally accented AI voices lifted brand recommendation more than a neutral human or neutral AI voice. | [S] | Low-Medium |
| Adobe Express survey (18 Feb 2026), 850 consumers, 205 marketers, via ContentGrip | 77% trust human voices most; 14% do not trust any voice they believe is AI; nearly three in four say brands should disclose AI voice or music. | [S] | Low-Medium (tool vendor) |
| NIQ (NielsenIQ), 12 Dec 2024. https://nielseniq.com/global/en/news-center/2024/niq-research-uncovers-hidden-consumer-attitudes-toward-ai-generated-ads/ (read) | 2,000+ viewers, EEG on about 150 | Viewers recognised most AI-generated **ads** and rated them "annoying", "boring", "confusing"; weaker memory activation even for high-quality AI ads; a possible "negative brand halo". This tests AI visuals and ads, not voice alone. | [D] | Medium |
| Raptive (2025), 3,000 US adults. https://raptive.com/blog/the-ai-stink-is-real-and-its-costing-brands/ (read) | Content suspected to be AI-generated was rated 48% less trustworthy, 57% less authentic, 60% lower in emotional connection; adjacent ads saw a 14% drop in purchase consideration. Ratings fell "regardless of whether it was really AI-generated or not". Publisher-commissioned. | [D] | Medium-Low |
| Finnish interview study (University of Jyväskylä, 2025), 18 consumers | Recognised AI voices trigger slow, analytical processing and often negative emotion; unrecognised ones are processed like human voices. | [R] | Low (n = 18) |

Pattern across these studies: **measured ad impact of a good synthetic voice is often equal to a human one; the penalty comes from
being perceived as AI.** The audience here (developers) has the highest detection and the least tolerance: 46% distrust AI accuracy and
favourability toward AI tooling has dropped by about ten points in two years (Stack Overflow 2025). A synthetic voice on a film about
AI coding assistants invites exactly that suspicion. A Dorylinae film that stays silent-first with strong type loses nothing and
avoids the penalty.

### 6.3 What developer-tool launches do

I found no study comparing voice-over, no voice-over and text-only developer-tool launch films, and I did not verify individual launch films.
What exists: developer-marketing writing favouring real product footage and one-line clarity (Mighty & True, Low); the Dropbox screencast case (4.1);
LinkedIn Creative Labs 2025 reporting named subject-matter experts speaking conversationally with up to 53% more engagement (via third-party copies, Low-Medium)
and the edX finding that informal personal delivery holds attention (Guo et al., Medium). The consistent signal is a **real, specific, personal voice or no voice;
a generic announcer voice is the weakest option**.

**Confidence for Q6: Medium-Low.**

---

## Q7. Aspect ratios and upload specs (2025 to 2026)

### 7.1 Aspect ratio per platform

| Platform | Recommended ratios | Notes | Source | Tag |
|---|---|---|---|---|
| YouTube | 16:9 (standard on desktop); vertical and square accepted, player adapts; no letterbox or pillarbox bars baked in. Shorts must be square or vertical and up to 3 minutes | Content for Shorts needs a separate cut | https://support.google.com/youtube/answer/1722171 ; https://support.google.com/youtube/answer/15424877 (both read) | [P] |
| LinkedIn (ads) | 16:9 (rec. 1920x1080 or 1200x675), **4:5 labelled "recommended ratio" (rec. 720x900, max 1080x1350)**, 1:1 (360x360 to 1920x1920), 9:16 (rec. 720x1280, max 1080x1920). Widest allowed is 16:9; a 5.33:1 file is rejected | Organic native allows roughly 1:2.4 to 2.4:1 (third-party) | https://www.linkedin.com/help/lms/answer/85306 (read) | [P] |
| X | **16:9 (landscape or portrait) or 1:1**; recommended 1280x720, 720x1280, 720x720; allowed range 1:3 to 3:1; 1:1 pixel aspect | Ads: 9:16, 16:9, 1:1 | https://docs.x.com/x-api/media/quickstart/best-practices (read) | [P] |
| Reddit | 1:1, 4:5, 4:3, 16:9. 2026 study: **4:5 drove significantly higher conversion than 1:1 or 16:9** on mobile | Reddit did not quantify the gap publicly | agency spec sheet (Mar 2026); ppc.land summary | [S] |
| Website hero | 16:9 landscape; poster image; essential message must not depend on the video | | web.dev / MDN | [B] |

### 7.2 Upload specs

| | YouTube | LinkedIn (ads) | X | Reddit (ads) |
|---|---|---|---|---|
| Container, codec | MP4, H.264 High profile, progressive, CABAC, closed GOP of half the frame rate, 2 consecutive B-frames, 4:2:0, moov atom at front ("fast start"), no edit lists | MP4; H.264 or VP8; no ProRes | MP4 or MOV in ads; H.264 High, YUV 4:2:0 only, progressive, no open GOP | MP4, MOV or M4V; no ProRes |
| Resolution | Native; 1080p is the reference | 16:9 max 1920x1080 (min 640x360); 4:5 max 1080x1350; 9:16 max 1080x1920; 1:1 max 1920x1920 | Recommended 1280x720 (also 720x1280, 720x720); allowed 32x32 to 1280x1024; Premium can get 1080p playback from a 1080p upload | 1080x1080, 1080x1350, 1440x1080, 1920x1080 given for images; no stated video pixel minimum |
| Frame rate | Keep the captured rate (24, 25, 30, 48, 50, 60); do not resample | "Less than 30 FPS" | 30 or 60 recommended; 60 max | 30 max (third-party) |
| Bitrate | SDR 1080p: **8 Mbps** at 24 to 30 fps, 12 Mbps at 48 to 60 fps; 1440p 16 / 24; 4K 35 to 45 / 53 to 68 | Not stated | Docs state min video bitrate 5,000 kbps and min audio 128 kbps, yet a table lists lower per-resolution values (conflict) | Not stated |
| Audio | AAC-LC (or Opus), 48 kHz, stereo 384 kbps recommended | AAC; SRT captions (text only) | AAC LC, mono or stereo, not HE-AAC | not stated |
| Colour | BT.709 for SDR | n/a | n/a | n/a |
| Max length | no hard limit relevant; Shorts 3 min | 3 s to 30 min (rec. 15 to 30 s; in-stream up to 90 s) | Organic 140 s (non-Premium); ads rec. 15 s, up to 2:20 | 5 s to 15 min (rec. 5 to 30 s) |
| Max file size | n/a | 75 KB to 500 MB | 512 MB organic (16 GB Premium); ads 1 GB, "keep under 30 MB" for best performance | 1 GB |
| Thumbnail | custom | JPG or PNG up to 2 MB, match video ratio | n/a | 400x300 min, matching ratio (third-party) |
| Source | support.google.com/youtube/answer/1722171 (P, read) | linkedin.com/help/lms/answer/85306 (P, read) | docs.x.com best-practices (P, read); help.x.com via search summaries (S) | Agency sheet (S) |

Conflicts flagged: LinkedIn's "less than 30 FPS" is ambiguous (30 or 29.97 may both pass; test on upload). X's help and API docs disagree on maximum length
(140 s on the app for non-Premium versus 20 minutes in the API docs) and on minimum bitrate. Reddit's own help page could not be loaded (JavaScript wall).

**Confidence for Q7: High for YouTube, LinkedIn and X (primary pages read); Medium for Reddit (agency copy of Reddit's sheet).**

---

## Evidence gaps and conflicts, said plainly

1. No controlled evidence on closed-beta recruitment copy, "apply" versus "waitlist", or developer-tool launch voice-over. Recommendations on these are inference.
2. Several first-party benchmarks (Vidyard, Think with Google, Reddit 2026, LinkedIn Creative Labs, B2B Institute) were only reachable as secondary summaries because the original pages block fetching. Numbers are as reported there.
3. Sound-off percentages differ by platform and year (85% Facebook 2016, 69% public-place survey 2019, 95% sound-on on YouTube 2018, 75 to 90% sound-on on Reels). The consistent part is "most feed video is first seen muted".
4. Scarcity studies are mostly hypothetical-choice experiments; field effect sizes are unknown.
5. Developer dislike data is qualitative and vendor-written; traceable numbers (Stack Overflow) show developers evaluate tools by trial and peers, which a film cannot offer but can point to.
6. Dates: much of the video-behaviour data is from 2016 to 2022. Directions have been stable across editions; magnitudes may have moved.

---

## Implications for our film

Everything below is a recommendation derived from the evidence above. Items marked (inference) go beyond direct evidence.

### 1. Length: one 60-second master and two cutdowns

- **Master: 60 seconds (55 to 65).** Inside the sub-minute bracket that holds the most viewers (Wistia 52%, Vidyard 66% finish), and long enough to explain an unknown product and show a real flow. LinkedIn Creative Labs reports 31 to 60 second films outperforming six-second ones (Medium-Low). Platforms allow it everywhere (X 140 s, LinkedIn 30 min, Reddit 15 min, YouTube any).
- **30-second cutdown** for LinkedIn and Reddit placements (LinkedIn recommends 15 to 30 s for ads, Reddit 5 to 30 s).
- **15-second cutdown** for X, where X recommends 15 seconds or less for branding impact, and for Reddit (shorter converts better in Reddit's 2026 study).
- **Optional 90 to 120 second walkthrough** for the landing page and YouTube, for people who already opted in (Wistia: 1 to 5 minute product demos hold over 50% engagement for educational content). Only if the owner wants a long form.
- Expect about half of starters to reach the midpoint. **The whole proposition (what it is, for whom, how to apply) must be understandable by second 40, and the call must be signalled before the midpoint.** (inference from the halfway drop-off and from Wistia's "first quarter" CTA advice for sub-minute video)

### 2. Structure and time shares (60 s master)

| Block | Seconds | Share | Job | Evidence behind it |
|---|---|---|---|---|
| Hook | 0 to 4 | about 7% | One concrete, recognisable situation shown, not told. Wordmark by second 3 to 5. At least 2 changes of shot or state. | Google ABCD (2+ shots, brand in 5 s), Reddit (logo in first or last 3 s), 1.7 s mobile glance, 50 to 70% loss between seconds 5 and 7 |
| Problem and plain-language "what it is" | 4 to 20 | about 27% | One sentence a stranger understands with no jargon; the pain it removes; for whom (small teams, each person with their own coding agent). | Radically new product: benefit and abstract first (Lee, Chu, Roy 2024); developers want "what do you do" in a line |
| Demonstration (features as a flow, not a list) | 20 to 46 | about 43% | Real UI and real terminal text, step by step: see who is online, send a request, it lands in the inbox, accept, result, signed record. Three capabilities at most, each shown in use. | Usage beats appearance (Frontiers 2022); process beats outcome (Ringler 2024); developer preference for real output |
| Trust and control | 46 to 52 | about 10% | The human stays in charge (approval on your own screen, sealed messages, a signed record). One honest limit if the owner permits. | Developer scepticism toward AI claims (Stack Overflow 2025: 46% distrust vs 33% trust) |
| The call | 52 to 60 | about 13% | One call, end card held 4 or more seconds. | Wistia CTA data; X "one exit point" |

So: **explaining about 35 percent (hook plus problem plus what it is), demonstrating about 45 percent, trust plus the call about 20 percent.**
A thin, persistent "Closed beta" tag from about second 20 and a one-line apply cue around the midpoint supports Wistia's finding that mid-video
CTAs convert better than end-only ones (22.1% versus 16.9%) without adding a second destination.

Cutdown proportions: 30 s = hook 3, what it is 7, demo 14, call 6. 15 s = hook 2, what it is 3, one-step proof 7, call 3.

### 3. Text versus voice

- **Silent-first. Every claim must be readable as burned-in type.** Most feed views start muted; SRT cannot carry the brand's typography; Reddit counts overlay text and captions at +8.2% conversion; caption users are mostly not hearing impaired.
- **No synthetic voice-over.** Measured performance is often equal, but perceived AI costs trust (Studio Resonate −23%, Raptive −48% trust for suspected AI content, NIQ memory penalty), the audience is the most AI-sceptical audience there is, and a TTS voice on an AI-assistant product is the worst version of that suspicion.
- **Do ship sound design (music bed, small interface sounds) as an enhancement, not as a carrier.** YouTube viewers are mostly sound-on (Google 2018, 95%). Do not rely on it for meaning.
- **Do not put the wordmark under the caption zone** (Verizon/Publicis: logo plus captions lowered memory for emotional or visual ads).
- Type rules: sentences short enough to read in the time shown; sentence case; no exclamation marks; one ant word per screen at most (brand voice). Keep on-screen words in the picture; if a second, sound-on cut is made later, do not read the same words aloud (redundancy effect) (inference).
- Put a transcript and a one-line text summary next to the film on the landing page, so the proposition does not depend on pressing play (accessibility guidance from vendor summaries, Low; the Nielsen Norman Group original could not be retrieved).

### 4. Formats to deliver

| File | Ratio and size | fps | Use |
|---|---|---|---|
| Master | 16:9, 1920x1080 | 30 | YouTube, website hero (click to play), X, LinkedIn desktop, Reddit desktop |
| Vertical-feed | **4:5, 1080x1350** | 30 | LinkedIn feed (LinkedIn labels 4:5 its recommended ratio) and Reddit mobile (Reddit 2026: 4:5 beats 1:1 and 16:9) |
| X variant | 16:9 downscaled to 1280x720 (X's recommended size; Premium only gets 1080p playback) | 30 | X; design type big enough to read at 720p |
| Optional | 1:1, 1080x1080, cropped from the 4:5 centre; 9:16 only if the owner wants Shorts or Reels | 30 | Compose the 4:5 so all essential type sits in the central 1:1 square and the 1:1 comes free. |

Encoding: H.264 High, progressive, 4:2:0, BT.709, MP4 with fast-start; AAC-LC 48 kHz stereo; **one frame rate (30) everywhere** because resampling
degrades quality (YouTube) and LinkedIn and Reddit cap at about 30. Give the encoder headroom: export masters at roughly 12 to 16 Mbps
for 1080p (YouTube's reference is 8 Mbps; platforms re-encode) (inference); keep an X variant under about 30 MB if it is ever used as an ad. Also deliver
a transcript text file and an SRT for YouTube accessibility. Poster frame: the frame where wordmark and the one-sentence claim are both visible.
Website hero: a separate silent 8 to 10 second loop with no audio track under about 4 to 5 MB, `muted autoplay loop playsinline`, a poster image, and
`prefers-reduced-motion` respected; the full film opens on click.

### 5. The call to action

- **One call, one destination, repeated identically**: the same words in the "Closed beta" tag, the mid-video cue and the end card.
  No second link, no @handles, no hashtags, no repo, no GitHub, no social icons in the frame (X advice; owner rule).
- **Phrase it as an application, because the mechanism is a reviewed invite.** Suggested register (copy for the owner to approve, brand voice, no hype):
  - "Closed beta. For teams of 2 to 5 who each work with their own coding agent."
  - "Invite only. Apply at [address to be confirmed by the owner]."
  - State the ask in one line: install one binary each, use it in place of chat for a month, tell us what breaks. (From the plan; owner to confirm wording and duration.)
- **Scarcity: state real capacity as a fact, never as a timer.** If the owner confirms the first-wave size is still ten teams, say it once.
  Do not use countdowns, "last chance" or "hurry" (dark-pattern evidence; developers will notice).
  The Superhuman precedent is the model: the queue exists because onboarding capacity is finite.
- **Do not describe unshipped features as available.** The setup, invite-redeem and feedback commands do not exist yet according to the repo; the call cannot say "run setup".
- **Do not plan for Hacker News.** Show HN excludes sign-up pages and landing pages, and asks creators to let people try the thing without sign-ups.
- Tell the right people up front what the product is not (Developers reward honesty about fit); a single line is enough.

### 6. Tone for a developer audience

- One sentence of plain description instead of category language; real commands and real output on screen; concrete numbers only if true and sourced from the repo docs.
- Banned in copy: revolutionary, seamless, supercharge, unleash, enterprise-grade, "AI-powered", any 10x claim. This matches the brand voice file.
- Avoid a brand-first cinematic open that withholds what the product is for more than a few seconds; the developer-discussion record disapproves of "a Netflix trailer for an API".

### 7. Questions for the owner (routes that could raise quality but need permission)

1. **A human voice.** Evidence favours a specific personal voice over a generic one (informal delivery holds attention; named experts engage more). Is a recorded human voice (the owner or a teammate, plain and unscripted) acceptable for an optional sound-on cut? This conflicts with "built entirely in code", so I will not do it without a yes.
2. **A person on screen** (hands on keyboard or a face). Google and LinkedIn data favour real people early. The brand reads as specimen and instrument. Is any human presence allowed?
3. **Scarcity facts.** May the film say the beta is invite-only and name the first-wave size (ten teams, 48-hour invite codes)? These are owner-approved spec items (D36), not shipped behaviour, and no dates are set.
4. **The "free" claim and the length of the commitment.** May the film say the beta is free and ask for about a month of use? Both come from the plan and handoff notes, not a published terms page.
5. **Where the CTA points.** The apply address and form must exist on publish day. The waitlist form is specified but not built.
6. **Harness names on screen.** Only Claude Code with Antigravity (agy) has a passed real cross-harness run. May the film name only those, or must it stay generic ("your coding agent")?
7. **Channel plan.** Organic native uploads (assumed here) versus paid placement changes the length targets (15 to 30 s for paid on LinkedIn and Reddit).

### Strength of the recommendations

| Recommendation | Strength |
|---|---|
| Sub-minute master, silent-first with burned-in type | High |
| Brand and subject visible by second 3 to 5, two-plus shots early | Medium |
| Lead with plain "what it is", then a real in-use process demo, not a feature list | Medium |
| One CTA; honest, capacity-based scarcity | Medium |
| No synthetic voice for this audience | Medium-Low (perception studies are vendor-run; the audience logic is inference) |
| 16:9 plus 4:5, 30 fps, H.264 | High |
| Exact time shares | Low (derived from the above; not tested anywhere) |

---

## Source list (by publisher; URL, year)

**Platform documentation read directly**
- LinkedIn Help, *Video ads advertising specifications*, https://www.linkedin.com/help/lms/answer/85306 (2026).
- YouTube Help, *Recommended upload encoding settings*, https://support.google.com/youtube/answer/1722171 (2026); *Understand three-minute YouTube Shorts*, https://support.google.com/youtube/answer/15424877 (2026); *Understand audience retention*, https://support.google.com/youtube/answer/9314415 (2026).
- X Developer docs, *Media best practices*, https://docs.x.com/x-api/media/quickstart/best-practices (2026).
- Hacker News, *Show HN guidelines*, https://news.ycombinator.com/showhn.html (2026).

**First-party datasets and reports**
- Wistia, *2022 State of Video Report*, https://wistia.com/about/state-of-video-2022 ; *How to Choose the Right Marketing Video Length for Any Goal*, https://wistia.com/blog/optimal-video-length (2025); *How to Add a Video CTA*, https://wistia.com/learn/marketing/using-video-ctas (2024 to 2025 data).
- Digiday, *85 percent of Facebook video is watched without sound*, https://digiday.com/media/silent-world-facebook-video/ (2016).
- Stack Overflow, *Developer Survey 2025, AI*, https://survey.stackoverflow.co/2025/ai ; *Developer Survey insights for B2B tech marketers*, https://stackoverflow.co/advertising/resources/stack-overflow-developer-survey-for-b2b-tech-marketers/ (year not stated).
- WPP Media, *AI voices in audio ads*, https://www.wppmedia.com/news/ai-voices-audio-ads (Oct 2025).
- Studio Resonate and CloudArmy, via ContentGrip, https://www.contentgrip.com/studio-resonate-ai-voice-study/ (Aug 2025).
- NielsenIQ, *Hidden consumer attitudes toward AI-generated ads*, https://nielseniq.com/global/en/news-center/2024/niq-research-uncovers-hidden-consumer-attitudes-toward-ai-generated-ads/ (Dec 2024).
- Raptive, *The AI stink is real*, https://raptive.com/blog/the-ai-stink-is-real-and-its-costing-brands/ (2025).

**Peer-reviewed**
- Guo, Kim and Rubin, *How video production affects student engagement*, ACM Learning@Scale 2014, https://www.cs.rochester.edu/hci/pubs/pdfs/edX-MOOC-video-production-and-engagement_LAS-2014.pdf
- Barton, Zlatevska and Oppewal, *Scarcity tactics in marketing*, Journal of Retailing 98(4), 2022, https://doi.org/10.1016/j.jretai.2022.06.003
- Lee, Chu and Roy, *Enhancing consumer evaluation of new products*, Journal of Business Research 182, 2024, https://ideas.repec.org/a/eee/jbrese/v182y2024ics0148296324002704.html
- Frontiers in Psychology 2022, product usage versus appearance videos, https://www.frontiersin.org/journals/psychology/articles/10.3389/fpsyg.2022.812579/full
- Ringler, Sirianni, Peck and Gustafsson, JAMS 52(2), 2024, https://ideas.repec.org/a/spr/joamsc/v52y2024i2d10.1007_s11747-023-00934-6.html
- Mathur et al., *Dark Patterns at Scale*, ACM CSCW 2019, https://arxiv.org/pdf/1907.07032
- Scheibehenne, Greifeneder and Todd, *Can there ever be too many options?*, Journal of Consumer Research 37(3), 2010.
- Mayer, Sobko and Mautone, *Social cues in multimedia learning: role of speaker's voice*, J. Educational Psychology 95, 2003; Mayer's handbook chapters on voice, modality, redundancy (via summaries); Wang, Xie and Li (2016) modality meta-analysis; Adesope and Nesbit (2012) redundancy meta-analysis; Craig and Schroeder (2019).

**Secondary summaries (originals not opened)**
- Vidyard benchmarks via https://www.marketingprofs.com/charts/2023/49949/business-video-benchmarks-retention-rates-by-length
- Verizon Media and Publicis (2019) via https://www.streamingmedia.com/Articles/News/Online-Video-News/80-of-Video-Caption-Users-Arent-Hearing-Impaired-Finds-Verizon-131860.aspx
- Google ABCD via https://ppc.land/mastering-youtube-advertising-with-the-abcd-framework/
- Reddit 2026 Creative Best Practices via https://ppc.land/reddits-2026-ad-creative-study-what-actually-moves-conversion-rates/ ; Reddit ad specs via https://mediamaxnetwork.com/wp-content/uploads/2026/03/Reddit-Specs.pdf
- LinkedIn Creative Labs 2025 via https://www.netinfluencer.com/?p=36939 ; B2B Institute and MediaScience 2023 via https://www.marketingweek.com/b2b-video-ads-fail-attention/
- Facebook IQ 2016 attention figures via Marketing Dive and ITWeb; Vidmob and LinkedIn via Search Engine Land.
- Dropbox, https://techcrunch.com/2011/11/01/founder-storie-how-dropbox-got-its-first-10-million-users/ (2011).
- Practitioner pieces: Mighty & True, https://mightyandtrue.com/insights/blog/what-developers-really-think-of-your-marketing-and-what-you-should-do-about-it/ (2026); Redis; daily.dev (numbers unsourced).
- AMA release on partial concealment, https://www.ama.org/2020/03/11/press-release-from-the-journal-of-marketing-the-art-of-product-concealment-in-online-retail/ (2020).
