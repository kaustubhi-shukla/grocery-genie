# Evaluation Framework: GroceryGenie

## What are evals and why do they matter?

**In plain English:** Evals are tests for AI features. Unlike regular software tests (does the button work? does the page load?), evals check whether the AI is giving *good enough* answers. "Good enough" is the key phrase — AI is probabilistic, not deterministic. The same receipt photo might be parsed slightly differently each time. Evals measure *how often* the AI gets it right.

**Why this matters for AI PMs:**
> In a 2025-2026 AI PM interview, you WILL be asked: "How do you evaluate AI quality in production?" The answer is evals. Traditional PMs measure feature adoption and conversion. AI PMs also measure model accuracy, confidence, and failure modes. Evals are the bridge between "we shipped a feature" and "the feature actually works."

**The three types of evals every AI PM should know:**

| Type | What it tests | When to run | Example |
|------|--------------|-------------|---------|
| **Offline evals** | Model accuracy on a curated test set | Before shipping, after prompt changes | "Does the receipt parser correctly extract 90% of items from our test receipts?" |
| **Online evals** | Model accuracy on real user data | Continuously in production | "Of the last 100 receipts users sent, how many needed corrections?" |
| **Human evals** | Subjective quality (tone, helpfulness) | Periodically, or after major changes | "Are the Saturday reminder messages clear and actionable?" |

## Eval 1: Receipt Scanning Accuracy

### What we're measuring
When a user sends a receipt photo, Gemini Vision extracts items, quantities, prices, and store name. We need to know: how often does it get this right?

### Test dataset
Build a set of 20-30 real receipt photos from Indian stores:
- 10 FirstClub receipts (primary use case)
- 5 supermarket receipts (Reliance Smart, DMart, More)
- 5 local vendor receipts (handwritten bills)
- 5 delivery app screenshots (Zepto, Blinkit order summaries)
- 5 challenging receipts (blurry, crumpled, partially cut off)

For each receipt, manually create the "ground truth" — the correct extraction:
```json
{
  "test_id": "receipt_001",
  "image": "receipt_samples/firstclub_001.jpg",
  "ground_truth": {
    "store_name": "FirstClub",
    "items": [
      {"name": "carrots", "quantity": 4, "unit": "pieces", "price": 40.0},
      {"name": "spinach", "quantity": 1, "unit": "bunch", "price": 25.0}
    ],
    "total": 65.0
  }
}
```

### Metrics

| Metric | Definition | Target | Why this target |
|--------|-----------|--------|-----------------|
| **Item recall** | % of real items that were extracted | >90% | Missing items means incomplete stock updates |
| **Item precision** | % of extracted items that are real (not hallucinated) | >95% | Phantom items corrupt inventory |
| **Quantity accuracy** | % of items where quantity is exactly right | >85% | Wrong quantities degrade stock tracking |
| **Price accuracy** | % of items where price is within Rs 5 | >80% | Price is nice-to-have; small errors are ok |
| **Store detection** | % of receipts where store is correctly identified | >90% | Critical for platform tracking |

### For the AI PM interview: precision vs recall

> **Recall** = "Of all the items on the receipt, how many did we find?"
> **Precision** = "Of all the items we extracted, how many actually exist on the receipt?"
>
> Why do we want higher precision (95%) than recall (90%)?
> Because a missed item (low recall) means the user adds it manually — annoying but harmless.
> A hallucinated item (low precision) means fake data enters the inventory — the user might not notice, and stock levels become wrong silently. **Silent errors are worse than visible gaps.**
>
> This is a core AI PM judgment call: which failure mode is more dangerous for your specific product?

### Eval script structure
```go
func evaluateReceiptScanning() {
    testCases := loadTestCases()
    var results []EvalResult

    for _, tc := range testCases {
        // Send image to Gemini Vision with our receipt scanning prompt
        extracted := parseReceipt(tc.Image)

        // Compare against ground truth
        scores := EvalResult{
            ItemRecall:       calculateRecall(extracted, tc.GroundTruth),
            ItemPrecision:    calculatePrecision(extracted, tc.GroundTruth),
            QuantityAccuracy: checkQuantities(extracted, tc.GroundTruth),
            PriceAccuracy:    checkPrices(extracted, tc.GroundTruth),
            StoreCorrect:     extracted.StoreName == tc.GroundTruth.StoreName,
        }
        results = append(results, scores)
    }

    printEvalReport(results)
}
```

## Eval 2: NLP Text Parsing Accuracy

### What we're measuring
When a user types "Made dal fry tonight, used 1 cup moong dal and 2 tomatoes," does Gemini correctly extract the intent, recipe, items, and quantities?

### Test dataset
Create 50+ text inputs covering:

**Happy path (30 cases):**
```json
{"input": "Used 2 carrots and 1 onion for pulao", "expected": {"action": "usage", "recipe": "pulao", "items": [{"name": "carrot", "qty": 2}, {"name": "onion", "qty": 1}]}}
{"input": "Bought 5kg rice and 2kg atta from FirstClub", "expected": {"action": "purchase", "platform": "FirstClub", "items": [{"name": "rice", "qty": 5, "unit": "kg"}, {"name": "atta", "qty": 2, "unit": "kg"}]}}
```

**Estimated items — no quantity provided (10 cases):**
```json
{"input": "Made biryani, used rice, oil, onions 3, tomatoes 2 and whole spices", "expected": {"action": "usage", "recipe": "biryani", "items": [{"name": "rice", "qty": null, "has_quantity": false}, {"name": "oil", "qty": null, "has_quantity": false}, {"name": "onion", "qty": 3, "has_quantity": true}, {"name": "tomato", "qty": 2, "has_quantity": true}, {"name": "whole spices", "qty": null, "has_quantity": false}]}}
{"input": "Used ghee, atta and salt for roti", "expected": {"action": "usage", "recipe": "roti", "items": [{"name": "ghee", "qty": null, "has_quantity": false}, {"name": "atta", "qty": null, "has_quantity": false}, {"name": "salt", "qty": null, "has_quantity": false}]}}
{"input": "Dal mein dal, ghee, jeera, salt, haldi daali", "expected": {"action": "usage", "recipe": "dal", "items": [{"name": "dal", "qty": null, "has_quantity": false}, {"name": "ghee", "qty": null, "has_quantity": false}, {"name": "jeera", "qty": null, "has_quantity": false}, {"name": "salt", "qty": null, "has_quantity": false}, {"name": "haldi", "qty": null, "has_quantity": false}]}}
```

**Relative quantities for estimated items (5 cases):**
```json
{"input": "Half the bottle of oil used making sweets", "expected": {"action": "usage", "items": [{"name": "oil", "qty_type": "relative", "relative_amount": 0.5}]}}
{"input": "Rice almost over", "expected": {"action": "usage", "items": [{"name": "rice", "qty_type": "relative", "relative_amount": 0.9}]}}
{"input": "Used a quarter of the ghee tin for halwa", "expected": {"action": "usage", "recipe": "halwa", "items": [{"name": "ghee", "qty_type": "relative", "relative_amount": 0.25}]}}
{"input": "Oil khatam ho gaya guests ke liye sweets banaye", "expected": {"action": "usage", "items": [{"name": "oil", "qty_type": "relative", "relative_amount": 1.0}], "is_guest_usage": true}}
{"input": "Used some oil for frying", "expected": {"action": "usage", "items": [{"name": "oil", "qty_type": "none"}]}}
```

**Outside meals + Kannada + confirmation (10 cases):**
```json
{"input": "Ordered pizza from Zomato, Rs 450", "expected": {"action": "outside_meal", "meal": "pizza", "platform": "Zomato", "cost": 450}}
{"input": "Had dinner outside at Meghana Foods", "expected": {"action": "outside_meal", "meal": "dinner", "platform": "Meghana Foods"}}
{"input": "Swiggy se biryani mangayi aaj", "expected": {"action": "outside_meal", "meal": "biryani", "platform": "Swiggy"}}
{"input": "ಎರಡು ಈರುಳ್ಳಿ ಮತ್ತು ಮೂರು ಟೊಮೇಟೊ ಬಳಸಿದೆ", "expected": {"action": "usage", "items": [{"name": "onion", "qty": 2}, {"name": "tomato", "qty": 3}]}}
{"input": "ಅರ್ಧ ಬಾಟಲಿ ಎಣ್ಣೆ ಬಳಸಿದೆ", "expected": {"action": "usage", "items": [{"name": "oil", "qty_type": "relative", "relative_amount": 0.5}]}}
```

**Edge cases (10 cases):**
```json
{"input": "Made the same paneer thing from last time", "expected": {"action": "usage", "recipe": "recall_previous", "items": "from_memory"}}
{"input": "3 tomatoes went bad had to throw", "expected": {"action": "waste", "items": [{"name": "tomato", "qty": 3}], "reason": "spoiled"}}
{"input": "Guests aa rahe hain Saturday, 6 log", "expected": {"action": "guest_event", "count": 6, "date": "Saturday"}}
{"input": "Aaj subah 2 ande aur bread use ki", "expected": {"action": "usage", "items": [{"name": "eggs", "qty": 2}, {"name": "bread", "qty": 1}]}}
```

### Metrics

| Metric | Target | Notes |
|--------|--------|-------|
| **Intent classification** | >95% | Must correctly identify: purchase, usage, waste, question, guest event |
| **Item extraction** | >90% | Correctly identify all items mentioned |
| **Quantity extraction** | >85% | Get the number and unit right (when quantity IS provided) |
| **Quantity-absent detection** | >90% | Correctly identify when NO quantity is given for an item (e.g., "used oil" vs "used 50ml oil"). Must NOT hallucinate a quantity when user didn't provide one |
| **Relative quantity parsing** | >85% | Correctly interpret "half the bottle", "almost over", "quarter of the tin" as relative amounts (0.5, 0.9, 0.25). Must distinguish from "none" (just "used oil") |
| **Guest/party context detection** | >85% | Detect when usage is guest-related ("made sweets for guests", "party mein oil use kiya") and flag for forecasting exclusion |
| **Outside meal detection** | >90% | Correctly identify outside meals ("ordered from Zomato", "had dinner outside") vs homecooked. Must extract platform name and cost if mentioned |
| **Kannada input parsing** | >75% | Parse Kannada text input ("ಎರಡು ಈರುಳ್ಳಿ ಬಳಸಿದೆ") into correct English entities. Lower target since this is Phase 6 and Gemini's Kannada is improving |
| **Receipt ambiguity detection** | >85% | Flag unclear quantities/units from receipt images for user confirmation instead of guessing |
| **Recipe detection** | >80% | Correctly identify recipe name when mentioned |
| **Hindi/Hinglish handling** | >75% | Parse code-mixed Hindi-English input |

### For the AI PM interview: why Hinglish matters

> India has ~500M internet users, and most communicate in code-mixed Hindi-English ("Hinglish"). If your AI product only handles clean English, you're excluding most of your TAM (Total Addressable Market). Testing for Hinglish isn't a nice-to-have — it's a market-sizing decision disguised as a technical requirement. This is the kind of insight that separates good AI PMs from great ones.

## Eval 3: Subscription Detection Accuracy

### What we're measuring
Given a purchase history, does the bot correctly identify repeating patterns? This is especially critical for **estimated items** (oil, rice, atta) where subscription detection is the PRIMARY restock signal.

### Test dataset
Create synthetic purchase histories:

```json
{
  "test_id": "sub_001",
  "description": "Regular rice buyer (estimated item — this is the primary restock signal)",
  "purchases": [
    {"item": "rice", "date": "2026-01-05", "qty": 5, "platform": "FirstClub"},
    {"item": "rice", "date": "2026-01-26", "qty": 5, "platform": "FirstClub"},
    {"item": "rice", "date": "2026-02-17", "qty": 5, "platform": "FirstClub"},
    {"item": "rice", "date": "2026-03-09", "qty": 5, "platform": "FirstClub"}
  ],
  "expected": {
    "is_subscription": true,
    "interval_days": 21,
    "confidence": 0.95,
    "platform": "FirstClub"
  }
}
```

**Guest/party exclusion cases (must exclude spikes from interval calculation):**
```json
{
  "test_id": "sub_003",
  "description": "Oil buyer with a Diwali party spike that should be excluded",
  "purchases": [
    {"item": "oil", "date": "2026-01-10", "qty": 5, "platform": "FirstClub", "guest_event": false},
    {"item": "oil", "date": "2026-02-05", "qty": 5, "platform": "FirstClub", "guest_event": false},
    {"item": "oil", "date": "2026-02-15", "qty": 10, "platform": "FirstClub", "guest_event": true},
    {"item": "oil", "date": "2026-03-02", "qty": 5, "platform": "FirstClub", "guest_event": false}
  ],
  "expected": {
    "is_subscription": true,
    "interval_days": 25,
    "note": "Feb 15 party purchase excluded from interval calc, so interval = avg(26, 25) = 25.5 ≈ 25"
  }
}
```

**Negative cases (should NOT be flagged as subscription):**
```json
{
  "test_id": "sub_005",
  "description": "Irregular mango buyer (seasonal)",
  "purchases": [
    {"item": "mango", "date": "2026-04-10", "qty": 2},
    {"item": "mango", "date": "2026-04-15", "qty": 3},
    {"item": "mango", "date": "2026-05-01", "qty": 2},
    {"item": "mango", "date": "2026-09-20", "qty": 1}
  ],
  "expected": {"is_subscription": false}
}
```

## Eval 3B: Platform Quality Scoring Accuracy

### What we're measuring
Does the bot correctly calculate effective cost (upfront cost + waste cost) per platform?

### Test dataset
```json
{
  "test_id": "quality_001",
  "description": "Tomatoes from two platforms with different waste rates",
  "purchases": [
    {"item": "tomato", "qty": 2, "price": 80, "platform": "FirstClub"},
    {"item": "tomato", "qty": 2, "price": 70, "platform": "Zepto"}
  ],
  "waste": [
    {"item": "tomato", "qty": 0.2, "source_platform": "FirstClub"},
    {"item": "tomato", "qty": 0.7, "source_platform": "Zepto"}
  ],
  "expected": {
    "firstclub": {"avg_price_per_kg": 40, "waste_rate": 0.10, "effective_cost": 44.4},
    "zepto": {"avg_price_per_kg": 35, "waste_rate": 0.35, "effective_cost": 53.8},
    "recommendation": "FirstClub (cheaper after waste)"
  }
}
```

### Metrics
| Metric | Target |
|--------|--------|
| Effective cost calculation accuracy | >95% (math, not AI — should be exact) |
| Correct platform recommendation | >90% (must pick actually cheaper option after waste) |
| Waste-to-purchase attribution | >85% (correctly linking wasted item to purchase platform) |

## Eval 4: Restock Prediction Accuracy

### What we're measuring
Two different prediction models, two different evals:

### 4A. Countable items (depletion-based)
When the bot says "carrots will run out in 2 days," how often is it right?

```
Given: Usage data from Day 1-20
Predict: When will carrots run out?
Check: When did carrots ACTUALLY run out (from Day 21+ data)?
Metric: Average prediction error in days
Target: Within 2 days of actual depletion
```

### 4B. Estimated items (interval-based)
When the bot says "oil reorder due in 5 days," how close is it to the actual next purchase?

```
Given: Purchase history up to Day N
Predict: When will user next buy oil?
Check: When did they ACTUALLY buy oil next?
Metric: Average prediction error in days
Target: Within 3 days of actual next purchase

Additional metric: Does excluding guest/party purchases improve accuracy?
  - Run prediction WITH party purchases included
  - Run prediction WITHOUT party purchases
  - Compare error rates
  - Target: guest-excluded model should be ≥15% more accurate
```

### For the AI PM interview: online vs offline evaluation

> **Offline eval** = Testing on historical/synthetic data before shipping. Controlled, repeatable, but may not reflect real-world messiness.
>
> **Online eval** = Measuring in production with real user data. Messy but truthful. For GroceryGenie, online eval means: every time we predict "carrots will run out Tuesday" and the user actually reports running out on Wednesday — that's a 1-day error we can track.
>
> **The ideal is both**: offline evals as a quality gate before shipping, online evals as continuous monitoring after shipping. This is called an "eval pipeline" or "ML monitoring."

## Eval 5: User Experience Quality (Human Eval)

### What we're measuring
Are the bot's messages clear, helpful, and appropriately toned?

### Rubric (score 1-5 for each)

| Dimension | 1 (Bad) | 3 (Okay) | 5 (Great) |
|-----------|---------|----------|-----------|
| **Clarity** | Confusing, jargon-heavy | Understandable but verbose | Crystal clear, concise |
| **Accuracy** | Wrong information | Mostly right, small errors | Precisely correct |
| **Actionability** | Tells you nothing useful | Informative but vague | Tells you exactly what to do |
| **Tone** | Robotic or annoying | Neutral | Friendly, appropriate |
| **Error handling** | Crashes or ignores errors | Generic "sorry" message | Helpful guidance to fix |

### Sample evaluation
```
Input: User sends a blurry receipt photo
Bot response: "I couldn't read this receipt clearly. Could you take another 
               photo with better lighting? Make sure the full receipt is visible."

Clarity:       5 (clear what went wrong)
Accuracy:      N/A
Actionability: 5 (tells user exactly what to do)
Tone:          5 (helpful, not blaming)
Error handling: 5 (graceful degradation)
```

## Running Evals: Process

### When to run evals
1. **Before first launch**: Establish baseline scores
2. **After every prompt change**: Did the change help or hurt?
3. **Weekly in production**: Track accuracy trends
4. **After adding new features**: Ensure nothing regressed

### For the AI PM interview: "eval-driven development"

> This is the AI-native equivalent of "test-driven development" (TDD). In TDD, you write tests before code. In eval-driven development, you write evals before prompts. The process:
> 1. Define what "good" looks like (write test cases with expected outputs)
> 2. Write the prompt
> 3. Run evals to measure accuracy
> 4. Iterate on the prompt until evals pass
> 5. Ship with confidence
>
> This matters because AI features can't be tested with traditional unit tests. A button either works or doesn't. An AI parser might work 85% of the time — evals tell you that number and help you improve it.

## Key AI PM Interview Topics (2026)

Beyond evals, these are the concepts you'll encounter while building GroceryGenie:

| Topic | What it means | Where you'll see it in this project |
|-------|--------------|-------------------------------------|
| **Prompt engineering** | Crafting AI instructions for reliable output | Receipt scanning and NLP parsing prompts |
| **Structured output** | Getting AI to return data in a specific format (JSON) | Every Gemini API call |
| **Multimodal AI** | AI that handles multiple input types (text + images) | Receipt scanning uses Vision, usage logging uses text |
| **Confidence scores** | AI self-reporting how certain it is | Receipt scan confidence drives "confirm with user" flow |
| **Eval pipelines** | Automated testing of AI quality | Our eval framework |
| **Prompt caching** | Reusing expensive prompt processing across calls | System prompts cached to reduce cost/latency |
| **Guardrails** | Preventing AI from doing wrong things | Confirming with user before logging, rejecting nonsensical inputs |
| **Human-in-the-loop** | Keeping humans in the decision loop | User confirms receipt scans, approves order suggestions |
| **Cold start problem** | System has no data for new users | First week: no usage patterns → can't predict depletion |
| **Data flywheel** | More usage → better predictions → more value → more usage | Recipe memory improves with every logged meal |
