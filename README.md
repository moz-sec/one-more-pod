# one-more-pod

限られた Node の CPU・メモリとゾーン条件を考えて Pod を配置する、日替わりスケジューリングゲームです。プレイ状態はブラウザごとに独立し、ランキングを共有します。ゲーム操作で実際の Kubernetes リソースは作成しません。

## 遊び方

1. 「デイリーチャレンジ」または「時間無制限で練習」を選びます。
2. 次々と現れる Pod の配置先を選びます。CPU は 1〜4、メモリは 2〜8 Gi の範囲で変化します。
3. 配置できない場合はスキップするか、配置済み Pod の × を押して取り外します。希望する Node で 1 個の取り外しで配置できる場合は、候補の「入替」から一度に入れ替えられます。他の Node に配置できるときは「入替候補を見る」で候補を展開します。得点差は入れ替え後の予測です。取り外した Pod は戻りません。
4. 本番は 1 分で終了します。「終了して結果へ」で途中終了することもできます。終了後に名前を入力し、ゲーム開始から 5 分以内に結果を登録してください。練習結果は登録されません。

「候補の得点内訳」を開くと、配置と入れ替えで増減する稼働点・連続配置・サービス構成などを比較できます。

キーボードでは `1`・`2`・`3` で対応する Node に配置し、`S` でスキップできます。入力欄を編集中やランキング表示中には操作されません。

問題は UTC 日付ごとに生成され、同じ日は全員が同じ問題に挑戦します。画面上部の「ランキング」から別ページを開けます。画面間の移動中もゲーム状態は維持されますが、**本番の制限時間は進み続けます**。再読み込みすると進行状態は消えます。

Pod は Envoy、Prometheus、OpenTelemetry Collector、Jaeger、Fluent Bit、CoreDNS を題材にしています。これはそれぞれを抽象化したゲーム上の Pod であり、実際にソフトウェアをデプロイしたり、接続や設定を検証したりするものではありません。

### スコア

| 条件 | 得点 |
| --- | --- |
| 最終的に残った Pod | 1 個につき 1,000 点 |
| Pod の配置 | 1 回につき 300 点。後から取り外しても残る |
| 連続配置 | 2 回目から +100、+200、+300、以降 +400 点 |
| Node の CPU またはメモリを使い切る | 最終状態の Node 1 台につき +300 点 |
| 3 台すべてに配置し、CPU 使用量の最大差が 2 以下 | 最終状態で +600 点 |
| 配置済み Pod を取り外す | 1 回につき −200 点 |
| サービス構成 | 下記の組み合わせにつき 1 回加点 |

スキップと取り外しでコンボは途切れます。得点の下限は 0 点です。

最終的な稼働 Pod とサービス構成を残しながら、多くの Pod を処理することも得点につながります。配置不能のとき、1 個外すと配置できる Pod を点線で示します。

### サービス構成ボーナス

最終状態で必要なプロジェクトがすべて残っていると加点します。異なる Node でも成立し、同じプロジェクトの重複配置による追加点はありません。必要なプロジェクトが最後の 1 個まで取り外されるとボーナスも消えます。複数の構成に同じ Pod を使えます。

| 構成 | プロジェクト | ボーナス |
| --- | --- | --- |
| トレース基盤 | OpenTelemetry + Jaeger | 800 点 |
| メトリクス基盤 | OpenTelemetry + Prometheus | 800 点 |
| プロキシ監視 | Envoy + Prometheus | 600 点 |
| DNS 監視 | CoreDNS + Prometheus | 600 点 |
| ログ収集 | Fluent Bit + OpenTelemetry | 600 点 |

組み合わせは [OpenTelemetry と Jaeger](https://tag-security.cncf.io/community/assessments/projects/jaeger/self-assessment/)、[OpenTelemetry Collector の Prometheus 連携](https://opentelemetry.io/docs/collector/configuration/)、[Prometheus による Envoy の監視](https://www.cncf.io/blog/2018/11/05/beginners-guide-cncf-landscape/)、[CoreDNS の Prometheus メトリクス](https://coredns.io/plugins/metrics/)、[Fluent Bit と OpenTelemetry の連携](https://www.cncf.io/blog/2024/12/04/fluent-bit-v3-2-building-on-best-in-class-performance-and-efficiency/)を参考にしたゲーム上の簡略化です。実際の連携には構成や設定が必要です。

## ローカルで動かす

Go 1.22 以上、Node.js 20.19 以上または 22.12 以上、npm、PostgreSQL が必要です。次はローカル開発用の PostgreSQL を Docker で起動する例です。

```sh
docker run -d --name one-more-pod-postgres \
  -e POSTGRES_USER=one-more-pod -e POSTGRES_PASSWORD=localtest -e POSTGRES_DB=one-more-pod \
  -p 127.0.0.1:15432:5432 postgres:18
```

```sh
cd web
npm ci
npm run build
cd ..
DATABASE_URL='postgres://one-more-pod:localtest@127.0.0.1:15432/one-more-pod?sslmode=disable' go run .
```

ブラウザで <http://localhost:8080> を開きます。ランキングを直接表示する URL は <http://localhost:8080/#/leaderboard> です。

フロントエンド開発時は、Go サーバーを起動したうえで別のターミナルから `cd web && npm run dev` を実行してください。Vite は `/api` を `localhost:8080` に転送します。

### 検証

```sh
go test ./...
cd web
npm test
npm run build
```

実際の PostgreSQL を使った統合テストは、テスト用 DB を起動したうえで、リポジトリのルートから次を実行します。

```sh
TEST_DATABASE_URL='postgres://one-more-pod:localtest@127.0.0.1:15432/one-more-pod?sslmode=disable' go test ./...
```

## コンテナと Kubernetes

```sh
docker build -t one-more-pod:local .
docker run --rm -p 8080:8080 -e DATABASE_URL='<コンテナから接続可能な PostgreSQL URI>' one-more-pod:local
```

既存の PostgreSQL を使い続ける場合は、これまでの `DATABASE_URL` を指定してください。テーブルと登録済みランキングをそのまま利用できます。

### OSS Showcase

[k8s/app.yaml](k8s/app.yaml) は `apps/one-more-pod/app.yaml` として OSS Showcase に配置する例です。Gateway は `gateway/showcase`、ホスト名は `one-more-pod.showcase.cloudnativedays.jp` を指定しています。同じ namespace 内に 1 インスタンス・1Gi の CloudNativePG Cluster を作成し、生成される `one-more-pod-db-app` Secret の `uri` をアプリへ渡します。掲載用リポジトリにコピーし、`image: one-more-pod:latest` を認証なしで pull できる実際の公開イメージに変更してから、Showcase の `python3 scripts/check.py` で確認してください。現在のイメージ指定はプレースホルダーで、そのままではクラスタから pull できることを保証しません。

旧名でデプロイ済みの場合、新しい Cluster は別のストレージを使用します。ランキングを引き継ぐ場合は、切り替え前に既存 DB からバックアップし、新しい DB に復元してください。

別の Kubernetes 環境では、イメージ、HTTPRoute の Gateway とホスト名を環境に合わせて変更してください。HTTPRoute と PostgreSQL の Cluster には、それぞれ Gateway API CRD と CloudNativePG Operator が必要です。マニフェストは進行中ゲームをメモリで管理するため、アプリを単一レプリカにしています。

## 構成と制約

- `web/`：React + TypeScript の画面とブラウザ内のゲーム状態
- `main.go`：Go の HTTP API、日替わり問題生成、操作履歴の検証
- `store.go`：PostgreSQL によるランキング保存
- `k8s/`：Deployment、Service、HTTPRoute の例

ランキングは PostgreSQL に保存され、アプリの再起動後も残ります。進行中のゲームは単一プロセスのメモリにあり、再起動で失われます。Showcase のクラスタや PVC が再作成された場合はランキングも失われる可能性があります。複数レプリカ間で進行中ゲームは共有されません。1 回の履歴は最大 8,192 操作です。サーバーは操作履歴を再実行して不正な配置と任意のスコアを拒否しますが、履歴の捏造や自動解答は防げません。操作ごとの受付時刻も検証しておらず、登録猶予中に履歴を捏造することも可能です。認証とレート制限は未実装です。

結果の送信後に通信が切れた場合、同じ名前と操作履歴で再送すると、重複登録せずに元の結果を返します。保存済みの結果はアプリの再起動後も再送できます。

## コントリビュート

不具合報告や変更提案には、再現手順、期待する動作、実際の動作を添えてください。変更時は上記のテストとビルドを実行してください。ゲームルールや得点を変える場合は、ブラウザとサーバーの両方の判定、テスト、この README を一緒に更新してください。エージェント向けの開発手順は [AGENTS.md](AGENTS.md) にあります。

## ライセンス

one-more-pod は [MIT License](LICENSE) で公開しています。

## ゲームデザインの参考

- Malone (1981), [Toward a Theory of Intrinsically Motivating Instruction](https://doi.org/10.1207/s15516709cog0504_2)：挑戦、好奇心、ファンタジーに関する考察。
- Ryan, Rigby & Przybylski (2006), [The Motivational Pull of Video Games](https://selfdeterminationtheory.org/SDT/documents/2006_RyanRigbyPrzybylski_MandE.pdf)：自律性、有能感、関係性とゲーム体験の関連。

これらは設計上の参考であり、実際の楽しさや難易度はプレイテストで確かめる必要があります。
