import { useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

import { evaluate, streamPod, type Action, type Game, type Node, type Pod } from './game';

type Entry = { player: string; score: number; scheduled: number; challenge: string };
const emptyGame: Game = { gameId: '', challenge: '', nodes: [], seed: 0, templates: [], recipes: [] };
const shortProject=(name:string)=>({OpenTelemetry:'OTel',Prometheus:'Prom', 'Fluent Bit':'Fluent',CoreDNS:'DNS',Jaeger:'Jaeger',Envoy:'Envoy'} as Record<string,string>)[name] || name;
async function api<T>(path: string, options?: RequestInit): Promise<T> { const res = await fetch(path, options); if (!res.ok) throw new Error(await res.text()); return res.json(); }
const post = (body?: unknown): RequestInit => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) });
function App() {
 const [game,setGame]=useState<Game|null>(null), [actions,setActions]=useState<Action[]>([]), [name,setName]=useState(localStorage.getItem('kubetris-name') || ''), [seconds,setSeconds]=useState(60), [endAt,setEndAt]=useState(0), [result,setResult]=useState<Entry|null>(null), [period,setPeriod]=useState('today'), [board,setBoard]=useState<Entry[]>([]), [error,setError]=useState(''), [busy,setBusy]=useState(false), [practice,setPractice]=useState(false), [feedback,setFeedback]=useState(''), [finished,setFinished]=useState(false), [personalBest,setPersonalBest]=useState<{day:string;score:number}|null>(()=>{try {const value=JSON.parse(localStorage.getItem('kubetris-best-v2') || 'null'); return typeof value?.day==='string' && typeof value?.score==='number' ? value : null;} catch {return null;}}), [newBest,setNewBest]=useState(false), [previousBest,setPreviousBest]=useState<number|null>(null);
 const [page,setPage]=useState(location.hash==='#/leaderboard'?'leaderboard':'game');
 const [boardError,setBoardError]=useState('');
 const [showSwaps,setShowSwaps]=useState(false);
 useEffect(()=>{ const onHashChange=()=>setPage(location.hash==='#/leaderboard'?'leaderboard':'game'); window.addEventListener('hashchange',onHashChange); return ()=>window.removeEventListener('hashchange',onHashChange); },[]);
 const refresh=()=>api<Entry[]>(`/api/leaderboard?period=${period}`).then(entries=>{setBoard(entries);setBoardError('');}).catch(()=>setBoardError('ランキングを取得できませんでした。'));
 useEffect(()=>{ refresh(); const id=setInterval(refresh,15000); return ()=>clearInterval(id); },[period]);
 useEffect(()=>{ if (!game || result || practice || finished) return; const tick=()=>{const remaining=Math.max(0,Math.ceil((endAt-Date.now())/1000));setSeconds(remaining);if(remaining===0) setFinished(true);}; tick(); const id=setInterval(tick,250); return ()=>clearInterval(id); },[game,endAt,result,practice,finished]);
 const start=async(mode: boolean = practice)=>{if(game && !finished && !result && (practice || Date.now()<endAt) && actions.length>0 && !window.confirm('進行中のゲームを終了して、最初から始めますか？')) return; const requestedAt=Date.now();setBusy(true);setFeedback('');setError('');try { const g=await api<Game>('/api/games',post()); setPractice(mode);setGame(g);setActions([]);setResult(null);setNewBest(false);setPreviousBest(null);setFinished(false);setEndAt(requestedAt+60000);setSeconds(60); } catch {setError('ゲームを開始できませんでした。サーバーへの接続を確認してください。');} finally {setBusy(false)} };
 const analysis=useMemo(()=>evaluate(game || emptyGame,actions),[game,actions]);
 const {placed,progress,combo,bonus,evictions,skipped,scheduled,allPods,loads,packed,balanced,activeProjects,completedRecipes,serviceBonus,points}=analysis;
 const ended=finished || (!practice && seconds===0) || actions.length>=8192;
 const pod=game && !ended && !result ? streamPod(game,progress) : undefined;
 const usage=(node:Node)=>loads[game!.nodes.findIndex(n=>n.id===node.id)];
 const reason=(node:Node)=>{if (!pod) return ''; const u=usage(node); if(pod.zone && pod.zone!==node.zone) return 'ゾーンが異なります'; if(u.cpu+pod.cpu>node.cpu && u.memory+pod.memory>node.memory) return 'CPU・メモリ不足'; if(u.cpu+pod.cpu>node.cpu) return 'CPU 不足'; if(u.memory+pod.memory>node.memory) return 'メモリ不足'; return '';};
 const unlockedByPlacement = pod ? game!.recipes.filter(recipe =>
  !completedRecipes.includes(recipe) && recipe.projects.every(name => name === pod.name || activeProjects.has(name))
 ) : [];
 const available=!!pod && game!.nodes.some(n=>!reason(n));
 const canMakeRoom=(resident:Pod,node:Node)=>{
  if (!pod || !reason(node) || (pod.zone && pod.zone!==node.zone)) return false;
  const used=usage(node);
  return used.cpu-resident.cpu+pod.cpu<=node.cpu && used.memory-resident.memory+pod.memory<=node.memory;
 };
 const hasRecovery=!!pod && !!game && game.nodes.some(node=>allPods.some(p=>placed.get(p.id)===node.id && canMakeRoom(p,node)));
 useEffect(()=>{setShowSwaps(false);},[game?.gameId,progress]);
 const evictionPreview=(resident:Pod,node:Node)=>{
  if (!game) return {delta:0,lost:[] as string[],swapDelta:null as number|null,swapLost:[] as string[]};
  const remove:Action={type:'evict',podId:resident.id};
  const afterRemoval=evaluate(game,[...actions,remove]);
  const lost=completedRecipes.filter(recipe=>!afterRemoval.completedRecipes.includes(recipe)).map(recipe=>recipe.name);
  if (!pod || !canMakeRoom(resident,node)) return {delta:afterRemoval.points-points,lost,swapDelta:null as number|null,swapLost:[] as string[]};
  const afterSwap=evaluate(game,[...actions,remove,{type:'place',podId:pod.id,nodeId:node.id}]);
  return {delta:afterRemoval.points-points,lost,swapDelta:afterSwap.points-points,swapLost:completedRecipes.filter(recipe=>!afterSwap.completedRecipes.includes(recipe)).map(recipe=>recipe.name)};
 };
 function explainMove(next: Action[]) {
  const after = evaluate(game!, [...actions, ...next]);
  const parts: [string, number][] = [
   ['稼働', (after.placed.size - placed.size) * 1000],
   ['配置', (after.scheduled - scheduled) * 300],
   ['連続', after.bonus - bonus],
   ['取り外し', (evictions - after.evictions) * 200],
   ['満載', (after.packed - packed) * 300],
   ['バランス', (Number(after.balanced) - Number(balanced)) * 600],
   ['サービス構成', after.serviceBonus - serviceBonus],
  ];
  return parts.filter(([, value]) => value !== 0)
   .map(([label, value]) => label + ' ' + (value > 0 ? '+' : '') + value.toLocaleString()).join('、');
 }
 const place=(node:Node)=>{
  if(!game || !pod || (!practice && Date.now()>=endAt) || result || reason(node)) return;
  const next:Action[]=[...actions,{type:'place',podId:pod.id,nodeId:node.id}];
  const outcome=evaluate(game,next), gained=outcome.completedRecipes.filter(recipe=>!completedRecipes.includes(recipe));
  const delta=outcome.points-points;
  setActions(next);
  setFeedback(gained.length ? `✦ ${gained.map(recipe=>recipe.name).join('・')} 完成！ +${delta.toLocaleString()} 点` : `${pod.name} を ${node.id} に配置！ ${delta>=0?'+':''}${delta.toLocaleString()} 点${combo>0 ? `（連続配置 ${combo+1}）` : ''}`);
 };
 const skip=()=>{if(pod && (practice || Date.now()<endAt) && !result) {setActions([...actions,{type:'skip',podId:pod.id}]); setFeedback(`${pod.name} をスキップ。次の Pod で立て直そう！`);}};
 const evict=(p:Pod)=>{if(game && pod && (practice || Date.now()<endAt) && !result) {const next:Action[]=[...actions,{type:'evict',podId:p.id}];const outcome=evaluate(game,next);const lost=completedRecipes.filter(recipe=>!outcome.completedRecipes.includes(recipe));const delta=outcome.points-points;setActions(next);setFeedback(`${p.name} を取り外し ${delta>=0?'+':''}${delta.toLocaleString()} 点${lost.length?` / ${lost.map(recipe=>recipe.name).join('・')} 解除`:''}`);}};
 const swap=(resident:Pod,node:Node)=>{
  if (!pod || !canMakeRoom(resident,node) || result || (!practice && Date.now()>=endAt) || actions.length+2>8192) return;
  const preview=evictionPreview(resident,node);
  const next:Action[]=[...actions,{type:'evict',podId:resident.id},{type:'place',podId:pod.id,nodeId:node.id}];
  const outcome=evaluate(game!,next), gained=outcome.completedRecipes.filter(recipe=>!completedRecipes.includes(recipe));
  setActions(next);
  setFeedback(`${resident.name} → ${pod.name} に入れ替え！ ${preview.swapDelta!>=0?'+':''}${preview.swapDelta!.toLocaleString()} 点${gained.length?` / ${gained.map(recipe=>recipe.name).join('・')} 完成！`:''}${preview.swapLost.length?` / ${preview.swapLost.join('・')} 解除`:''}`);
 };
 useEffect(()=>{
  const onKey=(event:KeyboardEvent)=>{
   if (!game || !pod || page!=='game' || event.repeat || event.isComposing || event.altKey || event.ctrlKey || event.metaKey) return;
   const target=event.target;
   if (target instanceof HTMLElement && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))) return;
   if (event.key.toLowerCase()==='s') { event.preventDefault(); skip(); return; }
   if (!/^[123]$/.test(event.key)) return;
   event.preventDefault();
   const node=game.nodes[Number(event.key)-1];
   if (!node) return;
   const why=reason(node);
   if (why) setFeedback(`${node.id} には配置できません：${why}`); else place(node);
  };
  window.addEventListener('keydown',onKey);
  return ()=>window.removeEventListener('keydown',onKey);
 });
 const submit=async()=>{if(!game || !name.trim()) {setError('プレイヤー名を入力してください。');return};setBusy(true);setError('');try { const entry=await api<Entry>('/api/results',post({gameId:game.gameId,player:name.trim(),actions}));setResult(entry);setPreviousBest(personalBest?.day===entry.challenge ? personalBest.score : null);const best=!personalBest || personalBest.day!==entry.challenge || entry.score>personalBest.score;if(best){const next={day:entry.challenge,score:entry.score};setPersonalBest(next);setNewBest(true);try {localStorage.setItem('kubetris-best-v2',JSON.stringify(next));} catch { /* ゲームの結果は保存済み */ }} try {localStorage.setItem('kubetris-name',name.trim());} catch { /* 名前を端末に保存できなくても結果は有効 */ } refresh();} catch {setError('スコアを登録できませんでした。登録期限が過ぎたか、ゲームが無効です。');} finally {setBusy(false)}};
 return <div className={'app '+(game?'playing':'')}><header><div className="brand"><span className="brand-icon">✣</span><span>one-more-<span>pod</span><small>SCHEDULER CHALLENGE</small></span></div><nav className="header-nav" aria-label="ページ移動"><a href="#/" aria-current={page==='game'?'page':undefined}>ゲーム</a><a href="#/leaderboard" aria-current={page==='leaderboard'?'page':undefined}>ランキング ↗</a></nav></header>
 <main>
 <div className="layout"><section className="game-panel" hidden={page!=='game'}><div className="panel-head"><div><div className="section-label">01 / PLAYGROUND</div><h2>Cluster overview <span>↗</span></h2></div><div className="challenge">CHALLENGE <strong>{game?.challenge || new Date().toISOString().slice(0,10)}</strong><small>UTC / 毎日更新</small></div></div>
 {!game ? <div className="intro"><div className="intro-icon">⬡</div><h3>Ready to schedule?</h3><p>次々に現れる Pod を 3 つの Node に配置。<br/>連続配置でコンボをつなげよう。<br/>詰まったらスキップや Pod の取り外しも可能！</p><div className="start-actions"><button className="primary" onClick={()=>start(false)} disabled={busy}>デイリーチャレンジ <span>↗</span></button><button className="practice-button" onClick={()=>start(true)} disabled={busy}>時間無制限で練習</button></div><details className="how-to-play"><summary>はじめての方へ：遊び方と得点</summary><ol><li>Pod の CPU・メモリ・ZONE を見て、置ける Node を選びます。</li><li>配置ごとに 300 点、最後まで稼働した Pod ごとに 1,000 点。連続配置とサービス構成でも加点されます。</li><li>置けないときはスキップ、または既存 Pod を取り外して入れ替えます。取り外しは減点、コンボはリセットされます。</li><li>本番は 1 分。終了後に名前を入力して、ゲーム開始から 5 分以内に結果を登録します。</li></ol><p>キーボード：1・2・3 で配置、S でスキップ。</p></details><div className="intro-meta"><span>◷ &nbsp; 本番は 1:00</span><span>▦ &nbsp; ENDLESS PODS</span><span>◎ &nbsp; 全員共通の問題</span></div>{personalBest?.day===new Date().toISOString().slice(0,10) && <p className="personal-best">TODAY’S BEST &nbsp; {personalBest.score.toLocaleString()} PTS</p>}</div> : <><div className="statusbar"><div><small>処理 / 稼働</small><strong>{progress} <span>/</span> {placed.size} <span>PODS</span></strong></div><div><small>SCORE</small><strong className="score-number">{points.toLocaleString()} <span>PTS</span></strong></div><div><small>COMBO</small><strong className="combo-number">×{combo}</strong></div><div className="timer-status"><small>{practice?'PRACTICE':'TIME'}</small><strong role="timer" aria-label={practice?'時間無制限':`残り ${seconds} 秒`} className={seconds<=20?'danger':''}>{practice?'∞':`${Math.floor(seconds/60)}:${String(seconds%60).padStart(2,'0')}`}</strong>{!practice && <div className="timer-track" aria-hidden="true"><div className={seconds<=20?'low':''} style={{width:`${seconds/60*100}%`}}/></div>}</div><button className="text-button" onClick={()=>setFinished(true)} disabled={ended || !!result}>■ 終了して結果へ</button><button className="text-button" disabled={busy} onClick={()=>start()}>↻ 最初から</button><button className="text-button mode-switch" disabled={busy} onClick={()=>start(!practice)}>{practice?'本番に挑戦 ↗':'練習モードへ'}</button></div>
 {result ? <div className="current success"><div><small>CHALLENGE COMPLETE</small><h3>{result.score.toLocaleString()} <span>PTS</span></h3><p>{newBest ? '自己ベスト更新！ ':''}{result.scheduled} Pod が稼働中。ランキングに登録しました。{previousBest!==null && ` 前回の自己ベスト ${previousBest.toLocaleString()} 点（${result.score-previousBest>=0?'+':''}${(result.score-previousBest).toLocaleString()}）`} <a href="#/leaderboard">順位を見る ↗</a></p></div><button onClick={()=>start()}>もう一度 →</button></div> : pod ? <div className={'current '+(!available?'blocked':'')}><div><small>NEXT POD <span>{available?'● READY TO SCHEDULE':'● NO AVAILABLE NODE'}</span></small><h3>{pod.name}<sup> / {pod.id.toUpperCase()}</sup></h3><p>{available?`${pod.role} ・配置先の Node を選択！`: hasRecovery?'候補を入れ替えるか、スキップできます。':'1 個の取り外しでは配置できません。スキップまたは複数の Pod を外そう。'}</p>{available && unlockedByPlacement.length > 0 && <p className="recipe-hint">配置で完成：{unlockedByPlacement.map(recipe => recipe.name).join('・')}（構成 +{unlockedByPlacement.reduce((sum, recipe) => sum + recipe.points, 0)} 点）</p>}<div className="current-actions"><button className="skip-button" onClick={skip}>スキップ ↷ <span>S キー / コンボ終了</span></button>{available && hasRecovery && <button className="toggle-swaps" onClick={()=>setShowSwaps(value=>!value)} aria-expanded={showSwaps}>{showSwaps?'入替候補を隠す':'入替候補を見る'}</button>}</div></div><div className="requirements"><span><small>CPU</small><b>{pod.cpu} <i>cores</i></b></span><span><small>MEMORY</small><b>{pod.memory} <i>Gi</i></b></span><span><small>ZONE</small><b>{pod.zone || 'ANY'}</b></span></div></div> : <div className="current"><div><small>{practice?'PRACTICE COMPLETE':'CHALLENGE COMPLETE'}</small><h3>{practice?'練習終了':'ゲーム終了'}：{placed.size} Pod 稼働中</h3><p>スキップ {skipped} / 取り外し {evictions}。{practice?'練習はランキングに登録されません。':'開始から 5 分以内に名前を入力して登録しよう。'}</p></div>{practice?<button onClick={()=>start(true)}>もう一度練習 →</button>:<form className="submit" onSubmit={event=>{event.preventDefault();if (!busy) void submit();}}><input required aria-label="プレイヤー名" maxLength={20} placeholder="プレイヤー名" value={name} onChange={e=>setName(e.target.value)}/><button type="submit" disabled={busy || !name.trim()}>{busy ? '登録中…' : '登録 →'}</button></form>}</div>}
 {ended && <div className="score-breakdown" aria-label="得点の内訳"><strong>SCORE BREAKDOWN</strong><span>稼働 {placed.size} × 1,000 <b>+{(placed.size*1000).toLocaleString()}</b></span><span>配置 {scheduled} × 300 <b>+{(scheduled*300).toLocaleString()}</b></span><span>連続配置 <b>+{bonus.toLocaleString()}</b></span><span>構成・配置ボーナス <b>+{(serviceBonus+packed*300+(balanced?600:0)).toLocaleString()}</b></span><span>取り外し {evictions} × −200 <b>−{(evictions*200).toLocaleString()}</b></span></div>}
 <div className="game-feedback" aria-live="polite"><span key={actions.length}>{feedback || 'Node を選んで配置を開始！'}</span></div>
 <div className="next-up">NEXT UP {(pod ? [1,2,3].map(offset=>streamPod(game,progress+offset)) : []).map(p=><span key={p.id}>{p.name} <b>{p.cpu}C / {p.memory}G{p.zone?' / '+p.zone.toUpperCase():''}</b></span>)}{!pod && '—'}</div>
 {pod && <details className="move-details" key={pod.id}><summary>候補の得点内訳</summary>{game.nodes.map(node=>{
 const removeTargets=allPods.filter(p=>placed.get(p.id)===node.id && canMakeRoom(p,node));
 const put:Action={type:'place',podId:pod.id,nodeId:node.id};
 return <div key={node.id}>{!reason(node) && <p>{node.id} に配置：{explainMove([put])}</p>}{removeTargets.map(p=><p key={p.id}>{node.id} の {p.name}（{p.id}）と入替：{explainMove([{type:'evict',podId:p.id},put])}</p>)}</div>;
 })}</details>}
 <div className="nodes">{game.nodes.map((node,i)=>{const u=usage(node), why=reason(node), valid=!!pod && (practice || seconds>0) && !result && !why;const residents=allPods.filter(p=>placed.get(p.id)===node.id);const placeDelta=valid ? evaluate(game,[...actions,{type:'place',podId:pod!.id,nodeId:node.id}]).points-points : null;return <div className={'node '+(valid?'valid':'')} key={node.id}><div className="node-top"><span className="node-icon">▦</span><div><strong>{node.id}</strong><small>ZONE / {node.zone.toUpperCase()}</small></div><span className="node-num">0{i+1}</span></div><div className="meter-label"><span>CPU</span><b>{u.cpu}{valid && <em className="incoming"> +{pod.cpu}</em>} <i>/ {node.cpu} cores</i></b></div><div className="meter"><div style={{width:`${u.cpu/node.cpu*100}%`}}/></div><div className="meter-label"><span>MEMORY</span><b>{u.memory}{valid && <em className="incoming"> +{pod.memory}</em>} <i>/ {node.memory} Gi</i></b></div><div className="meter purple"><div style={{width:`${u.memory/node.memory*100}%`}}/></div><button className="place-button" disabled={!valid} onClick={()=>place(node)}>{valid?`${i+1}　配置 +${placeDelta?.toLocaleString()} ↗`:pod && (practice || seconds>0) && !result?why:'ゲーム終了'}</button><div className="pod-list">{residents.length===0?<span className="no-pods">EMPTY NODE</span>:residents.map(p=>{const preview=evictionPreview(p,node); const delta=`${preview.delta>0?'+':''}${preview.delta.toLocaleString()} 点`; const details=`取り外し後 ${delta}${preview.lost.length ? `、解除: ${preview.lost.join('・')}` : ''}。コンボ終了`; const swapDelta=preview.swapDelta===null?'':`${preview.swapDelta>=0?'+':''}${preview.swapDelta.toLocaleString()}`; return <div className="pod-slot" key={p.id}><div className={'pod-chip '+((!available || showSwaps) && canMakeRoom(p,node)?'recovery':'')}><span className="pod-info" title={`${p.name} / CPU ${p.cpu} / Memory ${p.memory} Gi`}><span className="pod-name-full">{p.name}</span><span className="pod-name-short" aria-hidden="true">{shortProject(p.name)}</span><small>{p.cpu}C · {p.memory}G</small>{(!available || (showSwaps && canMakeRoom(p,node))) && pod && <small className="eviction-preview">外す {delta}{preview.lost.length>0 && <span className="lost-recipes"> / {preview.lost.join('・')}解除</span>}</small>}</span><button onClick={()=>evict(p)} disabled={!pod || (!practice && !seconds) || !!result} aria-label={`${p.name} ${p.id.toUpperCase()} を取り外す${canMakeRoom(p,node)?'（外すと配置可能）':''}、${details}`} title={details}>×</button></div>{(!available || showSwaps) && preview.swapDelta!==null && <button className="swap-button" disabled={actions.length+2>8192} onClick={()=>swap(p,node)} aria-label={`${p.name} を ${pod?.name} と入れ替え、得点 ${swapDelta} 点${preview.swapLost.length ? `、${preview.swapLost.join('・')}解除` : ''}、連続配置は1から`} title={`${preview.swapLost.length ? `${preview.swapLost.join('・')}が解除されます。` : ''}連続配置は1から`}>入替 {swapDelta}<small>連続配置 ×1</small></button>}</div>})}</div></div>})}</div>  <div className="dashboard-bottom"><div className="mission-board" aria-label="ボーナス目標"><div className="mission-heading">MISSION CONTROL <span>得点は最終配置で確定</span></div><div className="mission-list"><div className={packed>0?'achieved':''}>▣ リソース満載 <b>{packed} × 300</b><small>CPU またはメモリをぴったり使い切る</small></div><div className={balanced?'achieved':''}>◈ バランス <b>{balanced?'+600':'600 PTS'}</b><small>3 Node の CPU 使用量の差を 2 以下に</small></div></div></div> <div className="recipes" aria-label="サービス構成ボーナス"><div className="recipes-head">SERVICE COMBOS <span>最終配置で判定・Node は別々でも OK</span><b>+{serviceBonus.toLocaleString()} PTS</b></div><div className="recipe-list" tabIndex={0} aria-label="サービス構成の一覧。左右にスクロールできます">{game.recipes.map(recipe=>{const complete=completedRecipes.includes(recipe);return <div className={'recipe '+(complete?'achieved':'')} key={recipe.id} title={recipe.description}><div><strong>{complete?'✓ ':'◇ '}{recipe.name}</strong><b>+{recipe.points}</b></div><small>{recipe.projects.map((name,i)=><span key={name} className={activeProjects.has(name)?'present':''}>{i>0?' + ':''}{name}</span>)}</small></div>})}</div></div>
 </div><div className="game-hint">ⓘ &nbsp; 1 / 2 / 3 キーで配置、S キーでスキップ。Pod の × で取り外し（−200 点）。</div></>}{error && <p role="alert" className="error">{error}</p>}</section>
 <section className="leaderboard" hidden={page!=='leaderboard'}><a className="back-link" href="#/">← ゲームに戻る</a>{game && !result && !ended && seconds>0 && !practice && <p className="ranking-notice">ゲームの制限時間は進行中です。</p>}<div className="panel-head"><div><div className="section-label">02 / LEADERBOARD</div><h2>Top schedulers <span>↗</span></h2></div><span className="trophy">✳</span></div><p className="board-desc">世界中のプレイヤーとスコアを競おう。</p><div className="tabs"><button className={period==='today'?'active':''} onClick={()=>setPeriod('today')}>TODAY</button><button className={period==='all'?'active':''} onClick={()=>setPeriod('all')}>ALL TIME</button></div><div className="board-label"><span>RANK / PLAYER</span><span>SCORE</span></div>{boardError ? <div className="empty board-error" role="alert"><strong>{boardError}</strong><button onClick={refresh}>もう一度読み込む</button></div> : board.length===0 ? <div className="empty"><span>◈</span><strong>まだ記録がありません</strong><p>最初のスケジューラーになろう。</p></div> : <div className="entries">{board.map((e,i)=><div className="entry" key={`${e.player}-${i}`}><b className={i===0?'gold':''}>{String(i+1).padStart(2,'0')}</b><div><strong>{e.player}</strong><small>{e.scheduled} PODS RUNNING</small></div><div className="entry-score"><strong>{e.score.toLocaleString()}</strong><small>PTS</small></div></div>)}</div>}<div className="board-note"><span>✦</span><div><strong>HOW SCORING WORKS</strong><p>配置ごとに 300 点、残った Pod × 1,000 点。連続配置で +100〜400 点。ミッションとサービス構成も得点に加算し、取り外しは −200 点。</p></div></div>{error && <p role="alert" className="error">{error}</p>}</section></div></main></div>
}
createRoot(document.getElementById('root')!).render(<App/>);
