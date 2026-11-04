# CLAUDE.md

Bu repository'nin agent çalışma protokolü **`AGENTS.md`** dosyasıdır.
Claude Code bu dosyayı otomatik okuduğu için `AGENTS.md`, `CLAUDE.md` olarak
buradan içeri alınır. Aşağıdaki satır bir bağlantı değil, gerçek bir import'tur:

@AGENTS.md

## Bu dosya neden kuralların kendisini içermiyor

`AGENTS.md` araç bağımsızdır ve tek kaynaktır. Kuralların ikinci bir kopyası
buraya yazılsaydı iki dosya zamanla birbiriyle çelişirdi. Bu nedenle `CLAUDE.md`
yalnız yönlendirme yapar.

- Protokol değişikliği **`AGENTS.md`'ye** yazılır, buraya değil.
- Yeni doküman eklenince yolu ve kullanım amacı `AGENTS.md` içindeki
  dokümantasyon haritasına aynı değişiklikte eklenir.
- Bu dosya yalnız import hedefi veya Claude'a özgü bir kural gerekirse değişir.

## Oturuma başlarken

`AGENTS.md`'nin "Çalışmaya başlarken" bölümü esastır. Kısaca:
`git status --short --branch` → `PLAN.md` (kullanıcının kararları, gereksinim
durumu, sıradaki iş) → görevle ilgili kod ve testler. Sohbet geçmişi olmadan
devam edebilmek esastır; kritik bilgiyi yalnız sohbette bırakma.

**Teknik kararları kullanıcı verir; Claude uygular.** Kayıtlı bir karara
dayanmayan seçim çıkarsa koda başlamadan sor (bkz. `AGENTS.md` → "Rol").

Yerel doğrulama komutu: `cd backend; go test ./...` ve UI için `cd web; npm run build`.
