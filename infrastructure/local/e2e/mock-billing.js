const http = require('http');
// A stand-in for services/billing's card endpoints, for local end-to-end runs.
// It is not Stripe and it charges nothing: it remembers whether a card was
// "saved" and reports a fake visa 4242. Used to show the console's Billing
// screen working against something over HTTP with the bearer token.
let hasCard = false;
http.createServer((req, res) => {
  if (req.headers.authorization !== 'Bearer ' + (process.env.BILLING_API_TOKEN || 'bill-tok')) { res.writeHead(401); return res.end('unauthorised'); }
  res.setHeader('content-type', 'application/json');
  if (req.url.startsWith('/v1/billing/card-session')) {
    let b = ''; req.on('data', d => b += d); req.on('end', () => { console.log('SESSION', b); hasCard = true; res.end(JSON.stringify({ session_id: 'cs_1', url: 'https://checkout.stripe.com/c/pay/cs_1' })); });
  } else if (req.url.startsWith('/v1/billing/card')) {
    res.end(JSON.stringify(hasCard ? { configured: true, has_card: true, card: { brand: 'visa', last4: '4242', exp_month: 7, exp_year: 2030 } } : { configured: true, has_card: false }));
  } else { res.writeHead(404); res.end('no'); }
}).listen(Number(process.env.PORT || 8085), '127.0.0.1');
