# light-server

1つの常駐Goサーバーで、プロジェクトの静的ページ、JSONオブジェクト、アップロードファイルを扱います。自分用のlocalhost利用を想定しています。

## ビルドと起動

```sh
go build -o bin/light-server .
./bin/light-server serve --addr 127.0.0.1:8787
```

保存ルートは `$XDG_DATA_HOME/light-server`、未設定なら `~/.local/share/light-server`。`serve --data /absolute/path` で変更できます。同じ保存ルートの二重起動は拒否します。JSON本文は最大4MiB、バイナリはサイズ上限なし・ストリーミングです。HTTPヘッダー受信は10秒、アイドル接続は120秒。大容量転送のため本文の固定タイムアウトは設けません。停止時は処理の終了を最大30秒待ちます。

## プロジェクト管理

```sh
./bin/light-server project add demo --dir /absolute/path/to/output
./bin/light-server project list
./bin/light-server project update demo --dir /absolute/path/to/new-output
./bin/light-server project remove demo
./bin/light-server project list --all
./bin/light-server project add demo --dir /absolute/path/to/output
# このプロジェクトの保存データを完全削除する（配信元は削除しない）
./bin/light-server project purge demo
```

登録時に完全な閲覧URLとAPI URLが出ます。CLIは稼働中サーバーに接続します。別の接続先は各コマンド末尾の `--server http://127.0.0.1:8787` または `LIGHT_SERVER_URL` で指定できます。

配信元の編集は即反映します。内容を固定したい場合は別フォルダにコピーして登録してください。ページのリンクは相対URLまたは `/p/demo/` に対応するベースパスを使用してください。SPAフォールバックやルート絶対URLの書き換えは行いません。ディレクトリ一覧は出さず、index.htmlを配信します。配信元外へ出るシンボリックリンクは拒否します。保存ルートと重なるフォルダの登録も拒否します。

保存構造:

```text
保存ルート/projects/demo/
  project.json
  data.sqlite
  data.sqlite-wal（稼働状況による）
  data.sqlite-shm（稼働状況による）
  files/
```

登録解除では配信とデータAPIを停止して、このフォルダを保持します。同じIDで再登録すると再利用します。バックアップ機能はありません。稼働中のSQLiteファイルだけをコピーして完全なバックアップとは扱わないでください。

## JSON CRUDと検索

```sh
BASE=http://127.0.0.1:8787/api/projects/demo
curl -X PUT "$BASE/collections/reviews/objects/item-001" -H 'Content-Type: application/json' --data '{"status":"未確認","score":3,"memo":"確認する"}'
curl "$BASE/collections/reviews/objects/item-001"
curl "$BASE/collections/reviews/objects?limit=100&offset=0&sort=score&desc=true"
curl -X POST "$BASE/collections/reviews/search" -H 'Content-Type: application/json' --data '{"filters":[{"field":"status","op":"eq","value":"未確認"},{"field":"memo","op":"contains","value":"確認"}],"sort":"score","desc":true,"limit":100,"offset":0}'
curl -X DELETE "$BASE/collections/reviews/objects/item-001"
curl "$BASE/collections"
```

`POST .../objects` ではUUIDを自動発行して201を返します。`PUT .../objects/{id}` は作成または全置換で200を返します。本文はJSONオブジェクト限定。返却は `{id,data,created_at,updated_at}`、一覧・検索は `{items,limit,offset}`。コレクションは保存時に自動作成し、空になると一覧に出なくなります。同時更新は最後に成功した書き込みを採用し、部分更新・履歴はありません。

検索はトップレベルの項目に限定し、条件はAND、最大32個です。`eq` はnull・真偽値・数値・文字列に対応し、数値3と文字列"3"を区別します。nullと項目不存在も区別します。`contains` は大文字小文字を区別した文字通りの部分一致で、`%` や `_` は特別扱いしません。オブジェクト・配列の等値検索は対象外です。

並べ替えの `sort` は本文の項目名、または `$id`、`$created_at`、`$updated_at`。未指定時はID昇順、同値時もID昇順で安定化します。本文の混在型はSQLiteの順序（NULL/欠落、数値・真偽値、文字列、JSON配列/オブジェクトの文字列表現）に従います。降順では逆順です。欠落とnullは並び順では同等です。取得件数は既定100、最大1000、offsetは0以上。ページ間で更新が起きれば位置は変わりえます。

## バイナリ

```sh
curl -X PUT "$BASE/files/movie.mp4" --data-binary @movie.mp4
curl "$BASE/files/movie.mp4" -o downloaded.mp4
curl -I "$BASE/files/movie.mp4"
curl -H 'Range: bytes=0-99' "$BASE/files/movie.mp4"
curl "$BASE/files?limit=100&offset=0"
curl -X DELETE "$BASE/files/movie.mp4"
```

PUTは201を返し、同じキーなら全置換します。転送完了まで一時ファイルに書き、中断時は旧版を保持します。通常のGET/HEADとRangeに対応し、動画のシークやダウンロード再開に使えます。アップロード再開はありません。JSONからキーやURLを参照できますが、参照整合性や不要ファイルの自動削除はしません。

プロジェクトID、コレクション名、オブジェクトID、ファイルキーは英数字で始まる1〜128文字の英数字・`_`・`-`・`.`。ファイルの階層は作れません。検索項目はUnicodeを含め256バイトまで。

## ページのJavaScript例

`/p/demo/index.html` から使う例です。

```js
const project = location.pathname.split('/')[2];
const api = `/api/projects/${encodeURIComponent(project)}`;
const saved = await fetch(`${api}/collections/notes/objects/current`, {
  method: 'PUT',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({memo: '確認済み'})
}).then(r => { if (!r.ok) throw new Error(r.status); return r.json(); });
// input type=file などから取得したFileをそのまま送る
async function upload(file) {
  const response = await fetch(`${api}/files/${encodeURIComponent(file.name)}`, {
    method: 'PUT', body: file
  });
  if (!response.ok) throw new Error(response.status);
  return response.json();
}
```

## 管理HTTP API

- `GET /healthz`: 稼働確認
- `GET /api/projects?all=true`: プロジェクト一覧（allなしは稼働中のみ）
- `POST /api/projects`: `{ "id":"demo", "dir":"/absolute/path" }` で登録
- `GET /api/projects/demo`: 登録情報
- `PUT /api/projects/demo`: `{ "dir":"/absolute/path" }` で配信元更新
- `DELETE /api/projects/demo`: 登録解除
- `DELETE /api/projects/demo?purge=true`: 保存データを完全削除

エラーは `{ "error":"説明" }`。不正入力400、不在404、登録済み409、未対応メソッド405、保存障害500。DELETE成功は204です。認証・プロジェクト間のアクセス制限・広範なCORS許可は設けません。配信するページは自分の信頼できるものを使います。

## systemd常駐

`deploy/light-server.service` はユーザーサービスの例です。実行ファイルと保存先を実環境の絶対パスに変更してください。

```sh
mkdir -p ~/.config/systemd/user
cp deploy/light-server.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now light-server.service
loginctl show-user "$USER" -p Linger
# Linger=noならOS起動後・ログアウト後も動くよう有効化が必要
loginctl enable-linger "$USER"
systemctl --user status light-server.service
journalctl --user -u light-server.service -n 50
```

OS再起動の実試験は別途必要です。SSH越しの閲覧:

```sh
ssh -N -L 8787:127.0.0.1:8787 your-host
# 手元で http://127.0.0.1:8787/p/demo/ を開く
```

## 検証

```sh
go test -race ./...
go vet ./...
go build -o bin/light-server .
```

## 配布版とGitHub Actions

[Releases](https://github.com/olduvai-jp/light-server/releases) からmacOS arm64・Linux amd64/arm64・Windows amd64/arm64向けアーカイブを取得できます。展開した `light-server` を実行してください。`checksums.txt` にSHA-256を掲載します。Windows版はzip、それ以外はtar.gzです。

mainへのpush・PRでテスト、race検査、vet、5種類のビルドを実行し、Actionsのartifactに保存します。`v0.1.0` のようなタグをpushすると、成功したビルドからGitHub Releaseを自動公開します。手動ビルドはworkflow_dispatchでも実行できます。
