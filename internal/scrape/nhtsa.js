// Plain Playwright only: no stealth, proxies, CAPTCHA solving, or API substitution.
const SOURCE = 'https://www.nhtsa.gov/recalls';
const MAX_PDF = 20000000;

// Executed in Chromium. Success must come from a visible result container, never
// from the search input or the static FAQ's example zero-recall sentence.
function inspectResult(vin) {
  const visible = el => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
  document.querySelectorAll('[data-nhtsa-result]').forEach(e=>e.removeAttribute('data-nhtsa-result'));
  const body = document.body.innerText;
  if (/access denied|you don't have permission|verify you are human|unusual traffic|request blocked/i.test(body) ||
      [...document.querySelectorAll('iframe[src*="/bframe"], [id*="captcha-challenge"]')].some(el=>visible(el) && el.getBoundingClientRect().height>100)) {
    return {error:{code:'ACCESS_BLOCKED',message:'NHTSA denied access or requires a CAPTCHA'}};
  }
  const alerts = [...document.querySelectorAll('[role="alert"], [class*="error"], [id*="error"], #js-teleport-results-vehicle')].filter(visible).map(e=>e.innerText).join('\n');
  if (/no recall information found|unrepaired recall information was not found|invalid vin|vin.{0,60}(?:not found|not supported|not available|cannot be decoded)|unable to decode|manufacturer.{0,40}not supported/i.test(alerts)) {
    return {error:{code:'UNSUPPORTED_VIN',message:alerts.slice(0,1000)}};
  }
  if (/an unknown error occurred|temporarily unavailable|unable to (?:process|complete)|try again later|service unavailable/i.test(alerts)) {
    return {error:{code:'LOOKUP_FAILED',message:alerts.slice(0,1000)}};
  }
  const candidates = [...document.querySelectorAll('#vin-information, section, article, [id*="result"], [class*="result"]')].filter(visible);
  for (const el of candidates.sort((a,b)=>a.innerText.length-b.innerText.length)) {
    if (el.querySelector('#ymm-vin-recalls-search-input') || el.closest('form') || /faq/i.test(el.id+' '+el.className)) continue;
    const text=el.innerText;
    const vins=text.toUpperCase().match(/\b[A-HJ-NPR-Z0-9]{17}\b/g)||[];
    if (!vins.includes(vin) || vins.some(v=>v!==vin)) continue;
    const count=text.match(/\b(\d+)\s*unrepaired\s+recalls?\s*associated\s+with\s+this\s+VIN\b/i);
    const refresh=text.match(/(?:recall\s+data\s+(?:last\s+)?(?:refreshed|updated)|(?:last\s+)?(?:refreshed|updated)(?:\s+on)?)[\s:]*((?:\d{1,2}[\/\-]\d{1,2}[\/\-]\d{2,4})|(?:[A-Za-z]+\s+\d{1,2},?\s+\d{4}))/i);
    const vehicleHeading=el.querySelector('figure h1');
    const vehicleText=vehicleHeading ? vehicleHeading.innerText.replace(/\s+/g,' ').replace(/^((?:19|20)\d{2})(?=[A-Za-z])/,'$1 ') : text;
    const vehicle=vehicleText.match(/^(?:19|20)\d{2}[ \t]+[A-Za-z][A-Za-z0-9 .&/\-]+$/m);
    if (!count || !refresh || !vehicle) continue;
    // The chosen semantic result root is retained for expansion and printing.
    el.setAttribute('data-nhtsa-result','true');
    return {count:Number(count[1]),refreshDate:refresh[1],vehicle:vehicle[0].trim(),text};
  }
  return null;
}

async function capture(page, vin, lookupDate) {
  const deadline=Date.now()+60000;
  let result;
  while(Date.now()<deadline) {
    result=await page.evaluate(inspectResult,vin);
    if(result?.error) throw Object.assign(new Error(result.error.message),{code:result.error.code});
    if(result) break;
    await page.waitForTimeout(300);
  }
  if(!result) throw Object.assign(new Error('VIN-specific result, vehicle, refresh date, and recall count did not load'),{code:'RESULT_NOT_LOADED'});
  const root=page.locator('[data-nhtsa-result="true"]').last();
  // Expand only controls within the verified result, not page navigation/FAQ.
  for(let round=0;round<10;round++) {
    const controls=root.locator('[aria-expanded="false"], details:not([open]) > summary');
    const count=await controls.count();
    if(!count) break;
    let clicked=0;
    for(let i=count-1;i>=0;i--) {
      const c=controls.nth(i);
      if(await c.isVisible()) {await c.click({timeout:3000});clicked++;}
    }
    if(!clicked) break;
    await page.waitForTimeout(300);
  }
  const verified=await page.evaluate(inspectResult,vin);
  if(!verified || verified.error || verified.count!==result.count || verified.refreshDate!==result.refreshDate || verified.vehicle!==result.vehicle) {
    throw Object.assign(new Error('Result changed while capturing'),{code:'INCOMPLETE_RESULT'});
  }
  const printed=await page.evaluate(({vin,lookupDate,source,count})=>{
    const root=document.querySelector('[data-nhtsa-result="true"]');
    const text=root.innerText;
    const campaigns=text.match(/\b\d{2}[VE]\d{3}(?:\d{3})?\b/g)||[];
    if(count>0 && (new Set(campaigns).size<count || (text.match(/\bremedy\b/gi)||[]).length<count || (text.match(/\b(?:consequence|safety risk)\b/gi)||[]).length<count || (text.match(/\b(?:summary|description)\b/gi)||[]).length<count ||
       [...root.querySelectorAll('[aria-expanded="false"]')].some(e=>e.getClientRects().length))) {
      return {error:'Not all recall campaign details are visible'};
    }
    const limitations=[];
    for(const h of document.querySelectorAll('h2,h3,h4,h5,button')) {
      if(!/what (?:won.t|will) the (?:license plate and )?VIN search show/i.test(h.textContent)) continue;
      let block=h.parentElement;
      for(let i=0;i<3 && block && !block.querySelector('ul');i++) block=block.parentElement;
      if(block && block!==document.body && block.innerText.length<10000) limitations.push(block);
    }
    if(!limitations.some(e=>/already been repaired/i.test(e.textContent))) return {error:'NHTSA limitations did not load'};
    // A print-only clone preserves the rendered source text and detail order.
    const report=document.createElement('div');report.id='nhtsa-print-report';
    const title=document.createElement('h1');title.textContent='NHTSA VIN Recall Lookup';report.append(title);
    const meta=document.createElement('p');meta.textContent=`VIN: ${vin}\nLookup timestamp: ${lookupDate}\nSource: ${source}`;meta.style.whiteSpace='pre-wrap';report.append(meta);
    const clone=root.cloneNode(true);
    const originals=[root,...root.querySelectorAll('*')];
    const copies=[clone,...clone.querySelectorAll('*')];
    originals.forEach((el,i)=>{if(getComputedStyle(el).display==='none' || getComputedStyle(el).visibility==='hidden') copies[i].remove();});
    report.append(clone);
    const heading=document.createElement('h2');heading.textContent='NHTSA search limitations';report.append(heading);
    for(const el of new Set(limitations)) report.append(el.cloneNode(true));
    for(const el of report.querySelectorAll('script,style,iframe,form,nav,video,svg,img,.vin-search--find-a-dealer')) el.remove();
    for(const el of report.querySelectorAll('[id]')) el.removeAttribute('id');
    document.body.append(report);
    const css=document.createElement('style');css.textContent=`
      #nhtsa-print-report { display:none }
      @media print {
        @page {size:Letter; margin:16mm}
        body > *:not(#nhtsa-print-report) {display:none !important}
        html,body {display:block !important; height:auto !important; overflow:visible !important; margin:0 !important}
        #nhtsa-print-report {display:block !important;color:#000 !important;background:white !important;font:11pt/1.45 Arial,sans-serif !important}
        #nhtsa-print-report * {position:static !important;float:none !important; transform:none !important; height:auto !important;min-height:0 !important;max-height:none !important; width:auto !important;min-width:0 !important;max-width:100% !important;overflow:visible !important;white-space:normal !important;overflow-wrap:anywhere !important;color:#000 !important;background:transparent !important;box-shadow:none !important;font-size:11pt !important;line-height:1.45 !important}
        #nhtsa-print-report > p:first-of-type {white-space:pre-wrap !important}
        #nhtsa-print-report h1 {font-size:18pt !important}
        #nhtsa-print-report h2,#nhtsa-print-report h3 {font-size:14pt !important;break-after:avoid}
        #nhtsa-print-report p,#nhtsa-print-report li {orphans:3;widows:3}
        #nhtsa-print-report div,#nhtsa-print-report section,#nhtsa-print-report article {display:block !important;break-inside:auto !important}
        #nhtsa-print-report a {text-decoration:none}
      }`;document.head.append(css);
    return {text:report.innerText || report.textContent};
  },{vin,lookupDate,source:SOURCE,count:result.count});
  if(printed.error) throw Object.assign(new Error(printed.error),{code:'INCOMPLETE_RESULT'});
  await page.emulateMedia({media:'print'});
  await page.evaluate(()=>document.fonts.ready);
  const pdf=await page.pdf({format:'Letter',printBackground:true,preferCSSPageSize:true,tagged:true});
  if(pdf.length>=MAX_PDF) throw Object.assign(new Error('PDF exceeds 20 MB limit'),{code:'PDF_TOO_LARGE'});
  return {pdf,result};
}
async function lookup(vin,lookupDate) {
  const {chromium}=require('playwright');
  const browser=await chromium.launch({headless:true,channel:'chromium'});
  let closing;
  const close=()=>closing||(closing=browser.close());
  const onSignal=()=>{close().finally(()=>process.exit(1));};
  process.once('SIGTERM',onSignal);
  try {
    const userAgent=`Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${browser.version()} Safari/537.36`;
    const page=await browser.newPage({userAgent,locale:'en-US',viewport:{width:1280,height:900}});
    page.setDefaultTimeout(10000);
    const response=await page.goto(SOURCE,{waitUntil:'domcontentloaded',timeout:30000});
    if([401,403,429].includes(response?.status())) throw Object.assign(new Error('NHTSA denied access (HTTP '+response.status()+')'),{code:'ACCESS_BLOCKED'});
    const state=await page.evaluate(inspectResult,vin);
    if(state?.error) throw Object.assign(new Error(state.error.message),{code:state.error.code});
    const input=page.locator('#ymm-vin-recalls-search-input');
    await input.fill(vin);
    await input.locator('xpath=ancestor::form').getByRole('button',{name:/^search$/i}).click();
    return await capture(page,vin,lookupDate);
  } finally {await close();process.removeListener('SIGTERM',onSignal);}
}
if(require.main===module) {
  lookup(process.argv[2],process.argv[3]).then(({pdf})=>process.stdout.write(JSON.stringify({pdf:pdf.toString('base64')})))
  .catch(e=>{process.stdout.write(JSON.stringify({error:{code:e.code||'LOOKUP_FAILED',message:e.message.slice(0,1000)}}));process.exitCode=1;});
}
module.exports={inspectResult,capture,lookup};
