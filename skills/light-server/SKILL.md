---
name: light-server
description: light-serverの常駐サーバーに成果物フォルダを登録して閲覧URLを提供し、プロジェクト別のJSON CRUD・検索やバイナリ送受信を利用する。成果物の共有・レビュー画面の保存先が必要なときに使う。
---

# light-server

作業成果を既存の常駐サーバーから配信する。プロジェクトごとに開発サーバーを新しく立てる必要はない。静的HTMLとJavaScriptに向いており、SSRや任意のバックエンド実行には使わない。

## 接続と登録

1. `command -v light-server` と稼働確認を行う。サーバーURLは `LIGHT_SERVER_URL` があればそれを使い、なければ `http://127.0.0.1:8787`。
2. `curl -fsS "$server/healthz"` と `light-server project list --all --server "$server"` で状態と既存IDを確認する。既存プロジェクトを無断で上書きしない。
3. 成果物フォルダに `index.html` を用意し、プロジェクトIDを選んで登録する。リポジトリ全体ではなく、公開する成果物のフォルダを指定する。

```sh
server=${LIGHT_SERVER_URL:-http://127.0.0.1:8787}
light-server project add project-id --dir /absolute/path/to/output --server "$server"
# 登録済みプロジェクトの配信元を変更するときだけ
light-server project update project-id --dir /absolute/path/to/output --server "$server"
```

登録結果の `url` と `api` を使う。配信先は `/p/project-id/`、データAPIは `/api/projects/project-id`。配信元はサーバーが稼働する端末上のパスである。

- 相対リンクまたはプロジェクトのベースパスを使う。ルート絶対パス `/assets/...` の自動変換やSPAフォールバックはない。
- 編集は即反映。固定版が必要な場合は成果物を別フォルダへコピーして登録する。
- 配信元外へ出るシンボリックリンクと、保存ルートに重なる配信元は拒否される。
- 直接HTTPでページ・APIを確認し、JavaScriptを伴う画面では実際のブラウザでも操作を確認する。最終回答に閲覧URLを記載する。
- 別端末向けには `ssh -N -L 8787:127.0.0.1:8787 your-host` で転送し、手元のURLを案内する。localhost URLだけでは別端末から直接アクセスできない。

## ページからデータを保存する

JSONとバイナリはプロジェクト別に保持される。ページと同じオリジンのAPIを利用する。

```js
const project = location.pathname.split('/')[2];
const api = `/api/projects/${encodeURIComponent(project)}`;
async function request(path, options) {
  const r = await fetch(api + path, options);
  if (!r.ok) throw new Error(`${r.status}: ${await r.text()}`);
  return r.status === 204 ? null : r.json();
}
await request('/collections/notes/objects/current', {
  method: 'PUT', headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({memo: '確認済み'})
});
const saved = await request('/collections/notes/objects/current');
console.log(saved.data.memo);
```

JSON本文はオブジェクト、上限4MiB。コレクションは保存時に作成される。`PUT` は指定IDの作成・全置換、`POST .../objects` はUUIDを生成、`GET .../objects/{id}` は取得、`DELETE .../objects/{id}` は削除。PATCHはない。返却形式は `{id,data,created_at,updated_at}`。最後に成功した更新が残るので、共有オブジェクトの競合更新を前提にしない。

一覧は `GET .../collections/notes/objects?limit=100&offset=0`、検索は次の形。

```sh
curl -fsS "$server/api/projects/project-id/collections/notes/search" \
  -H 'Content-Type: application/json' \
  --data '{"filters":[{"field":"status","op":"eq","value":"未確認"},{"field":"memo","op":"contains","value":"確認"}],"sort":"$updated_at","desc":true,"limit":100,"offset":0}'
```

検索はトップレベルの項目、AND条件。`eq` は型を区別し、`contains` は大文字小文字を区別した文字通りの部分一致。limitは既定100、最大1000。並べ替えは本文の項目名または `$id`・`$created_at`・`$updated_at`。

## バイナリ

```sh
base="$server/api/projects/project-id"
curl -fsS -X PUT "$base/files/movie.mp4" --data-binary @movie.mp4
curl -fsS "$base/files/movie.mp4" -o downloaded.mp4
curl -fsSI "$base/files/movie.mp4"
curl -fsS "$base/files?limit=100&offset=0"
```

ページでは `fetch(api + '/files/movie.mp4', {method:'PUT', body:file})` のようにFileを直接送る。サイズ上限なしのストリーミング、同じキーへのPUTは全置換。GET/HEAD/Range対応。アップロードの途中再開はない。

プロジェクトID、コレクション名、オブジェクトID、ファイルキーは英数字で始まる1〜128文字の英数字・`_`・`-`・`.`。日本語ファイル名はそのままキーに使えないので、安全なキーへ変換し、元の名前が必要ならJSONに保存する。ファイル階層は作れない。

## 保持・運用

- `project remove ID` は配信登録解除。保存データは残り、同じIDで再登録すると再利用される。
- `project purge ID` はデータの完全削除。配信元は消さないが、保存済みJSON・ファイルは消える。成果物の配信依頼だけでは実行しない。
- データは保存ルートの `projects/ID/` 内の `data.sqlite` と `files/` に保持。SQLiteを直接編集せずAPIを使う。
- 認証やプロジェクト間のアクセス拒否はない。localhost/SSH転送を基本にし、依頼なく公開ネットワークへ待ち受けを変更しない。
- 未インストールの場合は [配布版](https://github.com/olduvai-jp/light-server/releases) からOS/アーキテクチャに合うものを選び、SHA-256を検証し、ユーザーのbinへインストールする。チェックアウト内のビルド出力を常駐サービスの実行元にしない。
- Linuxのユーザーサービスがある場合は `systemctl --user status light-server.service` で確認する。接続不能でも別ポートで代替サーバーを勝手に増やさず、既存設定とログを調べる。
- 詳細・導入手順: https://github.com/olduvai-jp/light-server/blob/main/README.md
