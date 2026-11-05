#!/usr/bin/env bash
# 44 出网与 pyworker 直连探测
set +e
export SUDO_PASS=dingsheng
cd /home/dingsheng/novatrader/src/deploy
source scripts/_lib.sh

probe() {
  local name="$1" url="$2"
  shift 2
  echo "== $name =="
  curl -sS -m 15 -o /tmp/probe.out -w "http=%{http_code} time=%{time_total}\n" "$@" "$url"
  echo "body_head=$(head -c 160 /tmp/probe.out 2>/dev/null)"
  echo
}

probe eastmoney_https "https://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=1&po=1&np=1&fltt=2&invt=2&fid=f3&fs=m:0+t:6&fields=f12,f14"
probe eastmoney_http  "http://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=1&po=1&np=1&fltt=2&invt=2&fid=f3&fs=m:0+t:6&fields=f12,f14"
probe sina "https://hq.sinajs.cn/list=sh600519" -H "Referer: https://finance.sina.com.cn"
probe tencent "https://qt.gtimg.cn/q=sh600519"
probe baostock_site "http://www.baostock.com"
probe akshare_doc "https://akshare.akfamily.xyz"

echo "== from nt-app wget eastmoney =="
docker exec nt-app wget -qO- -T 15 \
  "http://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=1&po=1&np=1&fltt=2&invt=2&fid=f3&fs=m:0+t:6&fields=f12,f14" 2>&1 | head -c 200
echo

echo "== from nt-pyworker curl eastmoney =="
docker exec nt-pyworker python - <<'PY'
import urllib.request
urls=[
 "http://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=1&po=1&np=1&fltt=2&invt=2&fid=f3&fs=m:0+t:6&fields=f12,f14",
 "https://hq.sinajs.cn/list=sh600519",
]
for u in urls:
  try:
    req=urllib.request.Request(u, headers={"User-Agent":"Mozilla/5.0","Referer":"https://finance.sina.com.cn"})
    with urllib.request.urlopen(req, timeout=15) as r:
      b=r.read(120)
      print(u.split("/")[2], "OK", r.status, b[:80])
  except Exception as e:
    print(u.split("/")[2], "FAIL", type(e).__name__, e)
PY

echo "== pyworker endpoints =="
for path in / /ak/macro /ak/global; do
  echo "---- GET $path ----"
  docker exec nt-app wget -qO- -T 60 "http://pyworker:18080$path" 2>&1 | head -c 300
  echo
done

echo "== baostock login smoke in pyworker =="
docker exec nt-pyworker python - <<'PY'
import baostock as bs
r=bs.login()
print("login", r.error_code, r.error_msg)
# recent trading day bars for 600519
import datetime
d=(datetime.date.today()-datetime.timedelta(days=5)).strftime("%Y-%m-%d")
e=datetime.date.today().strftime("%Y-%m-%d")
rs=bs.query_history_k_data_plus("sh.600519","date,code,close",start_date=d,end_date=e,frequency="d",adjustflag="3")
rows=[]
while rs.error_code=="0" and rs.next():
  rows.append(rs.get_row_data())
print("bars", len(rows), rows[-1] if rows else None)
bs.logout()
PY

echo "== DONE =="
