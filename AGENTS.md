# Vodafone-Case çalışma protokolü

## Amaç ve kapsam

Bu repository, **AI Platform Engineer — Take-Home Case** teslimidir: gerçek bir
model üzerinde çalışan küçük bir agent platformu ve bu platformla tanımlanmış,
uçtan uca çalışan bir örnek agent. Agent ve tool'lar veridir, koşular canlı
akar, riskli adımlar insan onayı bekler.

Değerlendirmede ağırlık **platform katmanının doğru, güvenli ve basit** olmasıdır;
arayüzün görünüşü puanlanmaz. **Önceliklendirme de değerlendirmenin parçasıdır**:
çekirdek, her şeyi mükemmelleştirmekten önce gelir. Yetişmeyen ve bilerek
dışarıda bırakılan her şey gerekçesiyle `DESIGN.md`'ye yazılır.

- Süre: 7 gün. Başlangıç 2026-11-02 varsayıldı (repo oluşturma tarihi; kesin
  teslim tarihi kullanıcıdan gelirse `PLAN.md`'ye yazılır).
- Kaynak: `AI-Platform-Engineer-Case-EN.pdf` (kullanıcıda; repoya kopyalanmaz).
  Gereksinimlerin özeti aşağıdadır; çelişkide PDF geçerlidir.

## Rol: kararlar kullanıcının, uygulama agent'ın

Bu bir case study'dir ve değerlendirilen kişi kullanıcıdır. **Bütün teknik
kararları kullanıcı verir; agent bu kararları koda döker.** (Kullanıcı kararı,
2026-11-02. Possessio'daki "teknik özerklik" modeli burada geçerli değildir.)

**Kullanıcının kararı olanlar** (liste örnektir, eksiksiz değildir):
dil ve framework; kütüphane/bağımlılık ekleme; mimari ve paket yapısı; veri
modeli ve şema; API/endpoint biçimi; canlı akış protokolü (SSE/WebSocket);
kimlik doğrulama ve şifreleme mekanizması; hata, timeout, yeniden deneme ve limit
politikaları; provider ve model; örnek agent; test yaklaşımı; dal/commit akışı;
`README.md`'deki varsayımlar ve `DESIGN.md`'deki her karar.

**Agent'ın yaptığı:**

- Kullanıcının kararını olduğu gibi uygular. Talimatta bulunmayan ve yukarıdaki
  türden bir seçim çıkarsa **durur ve sorar**; seçenekleri kısa artı/eksileriyle
  sunar. Tavsiye istenirse verir, ama kararı verilmiş gibi uygulamaz ve
  "şimdilik böyle yaptım" diye ilerlemez.
- Yalnız kararın içindeki uygulama ayrıntısı agent'a kalır: yerel adlandırma, bir
  fonksiyonun iç düzeni gibi arayüz, bağımlılık, şema veya davranış
  değiştirmeyen şeyler. Bir ayrıntının karar olup olmadığından emin değilse sorar.
- Kullanıcının bir kararı gereksinim ihlali, güvenlik açığı veya hata
  doğuracaksa bunu açıkça ve somut örnekle söyler. Kullanıcı kararında ısrar
  ederse karar uygulanır ve risk kayda geçer; sessizce "düzeltme" yapılmaz.
- Kullanıcı her satırı **açıklayabilmeli ve değiştirebilmelidir** (case kuralı).
  Bu yüzden kod düz ve izlenebilir yazılır; kullanılmayan genellik ve açıklanması
  zor soyutlama yoktur. Her değişiklikten sonra neyin, nerede, neden öyle
  yapıldığı kısaca anlatılır.
- Verilen kararlar tarih ve kullanıcının kısa gerekçesiyle `PLAN.md` → "Kararlar"
  bölümüne yazılır. `DESIGN.md` bu kayıttan derlenir; agent kullanıcının vermediği
  bir kararı oraya yazmaz.
- Dış işlemler (kullanıcının hesabıyla GitHub issue/gist açmak, repo oluşturmak,
  değerlendiricilere erişim vermek, belirlenen bütçeyi aşan harcama) somut
  kullanıcı onayı ister.

Repository kararlarında öncelik: güncel kullanıcı talebi ve kullanıcının
`PLAN.md`'de kayıtlı kararları → case PDF'i → bu protokol.

## Gereksinim kontrol listesi

Her madde kod + test + doküman ile kapanır. Durum ve kanıt bağlantısı `PLAN.md`'de
tutulur; burada yalnız PDF'ten gelen sözleşme vardır.

| # | Gereksinim | Kabul özü (PDF) |
| --- | --- | --- |
| R1 | Agent'lar veridir | ad, system prompt, model, izinli tool listesi; yeni agent kod değişikliği istemez |
| R2 | Tool = HTTP istek şablonu | method, URL, header, body, parametre JSON şeması, `{{parameter}}` yer tutucuları; model parametreyi doldurur, platform isteği atar ve cevabı modele verir; tool'un kendi API anahtarı modele görünmez |
| R3 | Agent döngüsü hatayı yönetir | bilinmeyen tool, şemayı bozan parametre, bozuk JSON ve kaçak döngüler ele alınır |
| R4 | Koşular canlı akar | model çıktısı, tool çağrısı ve sonucu tarayıcıya anında (SSE veya WebSocket); her koşu `succeeded` / `failed` / `cancelled` ile biter — provider hatası, timeout ve kullanıcının sayfayı kapatması dahil |
| R5 | Riskli adım onay bekler | "requires approval" tool çağrılınca koşu durur; kullanıcı isteği parametreleriyle görür, onaylar/reddeder, koşu buna göre devam eder; bekleyen onay sunucu yeniden başlatmasından sağ çıkar |
| R6 | Takımlar izoledir | ≥2 takım, her birinde kullanıcı; basit login yeterli; agent/tool takıma aittir; kullanıcı başka takımın agent'ını göremez/çalıştıramaz — sunucu denetler |
| R7 | Kullanıcı kendi model anahtarını getirir | koşu, başlatanın anahtarıyla çalışır; anahtar şifreli saklanır; log, kayıt ve canlı akışta görünmez; anahtarı olmayan kullanıcı açık hata alır; başkasının anahtarına asla düşülmez |
| R8 | Provider-agnostik ve ölçülü | ≥2 model sağlayıcı; agent'ın modeli bir ayardır; HTTP 429'da düzgün davranır; her model çağrısının token kullanımı kaydedilir, kullanıcı ve takım toplamları ekranda veya endpoint'te |
| EA | Örnek agent | ≥2 gerçek API tool'u; ≥1 gerçek yan etkili adım onay akışından geçer (ör. kendi test repomuzda issue/yorum/gist); platform üzerinden (API/UI) tanımlanır, hard-code değil |

**Nice-to-have** (PDF): iptal, agent zincirleme, tool cevabından gelen prompt
injection koruması, izole kod çalıştırma, MCP tool kaynağı, davranış değişikliğini
yakalayan eval seti, çoklu replika canlı akış. Nice-to-have gereksinimin yerini
tutmaz; hangisinin ne zaman yapılacağı kullanıcının kararıdır.

## Teslimatlar

| Teslimat | İçerik (PDF) |
| --- | --- |
| Git geçmişi | commit geçmişi korunur; "oraya nasıl vardığınızı" görmek istiyorlar. Private GitHub/GitLab repo veya `.git` içeren zip |
| `README.md` | kurulum ve çalıştırma, kullanılan provider ve model, örnek agent'ın çalıştırılması, varsayımlar. Docker'sız, birkaç komutla, **10 dakikanın altında** ayağa kalkmalı |
| `DESIGN.md` | **en fazla 2 sayfa**: mimari, kararlar ve alternatifleri, bilerek dışarıda bırakılanlar, güvenlik riskleri, binlerce geliştiriciye açılınca neyin değişeceği; Go dışı dil seçilirse gerekçesi; framework kullanılırsa neden/ne yaptı/nerede engel oldu |
| `AI_USAGE.md` | hangi AI araçları nerede kullanıldı; hangi önerileri reddettik veya düzelttik |
| Testler | en az agent döngüsü, tool şablonları, takım yetkileri, onay akışı |
| `examples/` | bir kayıtlı koşu: görev, akan olaylar, onay, nihai cevap |
| Ekran kaydı | ≤5 dk, örnek agent platformda çalışırken, onay adımı dahil (kullanıcı çeker) |

## Kararlar: verilenler ve açık olanlar

Asıl kayıt `PLAN.md` → "Kararlar" bölümüdür; burada yalnız özet durur.

**Verilmiş (kullanıcı):**

- Birincil model provider'ı **OpenAI API** (2026-11-02). Case para harcamayı şart
  koşmuyor, yalnız beklemiyor; gerçek çağrılar kullanıcının OpenAI anahtarıyla yapılır.
- `DESIGN.md` ve `PLAN.md` İngilizce (2026-11-02).
- Backend Go; veritabanı SQLite + `modernc.org/sqlite`; UI Next.js (kurulum süresi
  sonda yeniden değerlendirilir); provider'lar OpenAI (ana) ve Anthropic (2026-11-02,
  B1 — Codex danışmasından önce). Agent katmanı: Go içinde kendi döngümüz,
  `openai-go` + `anthropic-sdk-go`, framework ve Python servisi yok. Ayrıntı `PLAN.md` D5–D9.
- Hata sebebine göre yeniden deneme (D10), tool şablonlarında hedef/redirect/sır
  sızıntısı koruması (D11), anahtarların kullanıcı başına DB'de tutulup her çağrıda
  açıkça verilmesi (D12), ham Codex logları git dışında ve `AI_USAGE.md` canlı (D13).
- B2 (D14–D33): AES-256-GCM + env master key, DB'de bcrypt'li kullanıcılar ve
  `.env`'den seed şifreleri, sunucu oturumu, HttpOnly cookie ve CSRF koruması
  (`SameSite=Lax`, `Origin` kontrolü), model anahtarı UI'dan, **kullanıcıya ait ve hosta bağlı** tool sırları
  (yalnız header, koşuyu başlatanın sırrı kullanılır), önizleme/kayıtlar baştan
  sırsız + cevapta maskeleme, normalize mesaj tablosu ve koşu başı snapshot (sır
  ID'siyle), koşullu onay güncellemesi ve onayı yalnız başlatan verir, kullanıcı
  başına sahiplik + takım içi paylaşım. Ayrıntı `PLAN.md`.
- Plan kararları batch'ler hâlinde Codex `gpt-6.1-sol`'a danışılır; istem ve
  cevaplar repoda `docs/reviews/` altında tutulur (2026-11-02). Yöntem aşağıda.

**Açık (kullanıcı kararı bekliyor).** Parantez içindekiler PDF'teki kısıtlar veya
agent'ın notlarıdır; karar değildir:

- Canlı akış protokolü: SSE mi, WebSocket mi.
- Retry/backoff, timeout, adım sınırı ve tool cevabı boyut limiti politikaları.
- Kullanıcı sayfayı kapatınca koşunun ne olacağı (devam mı, iptal mi; her iki
  durumda da R4 son durum istiyor).
- Yan etkili tool çağrısı timeout'a düşerse ne yapılacağı.
- Örnek agent (PDF fikirleri: issue triage, dependency check, release notes).
- Hangi nice-to-have'lerin yapılacağı.
- Dal ve merge akışı.
- Değerlendiriciye görünen diğer metinlerin dili (README, AI_USAGE, kod yorumları; commit mesajları İngilizce — D36).
- Push öncesi kod incelemesi yapılıp yapılmayacağı (plan danışması karara bağlandı, kod incelemesi değil).
- Bütçe tavanı (verilirse `PLAN.md`'ye yazılır ve aşılmaz).

Bir açık karar verilince bu listeden "verilmiş"e taşınır ve `PLAN.md`'ye kaydedilir.
Açık kararların hangi batch'te ele alınacağı `PLAN.md` → "Planning batches"te.

### Codex (`gpt-6.1-sol`) danışması

Her batch için akış: agent seçenekleri ve artı/eksilerini sunar → kullanıcı karar
verir → batch Codex'e danışılır → kullanıcı son kararı verir → `PLAN.md`'ye yazılır.

- İstem `docs/reviews/<YYYY-MM-DD>-<batch>/prompt.md`, cevap aynı klasörde
  `answer.md`. İstem bağlamı (case gereksinimleri, önceki kararlar), danışılan
  kararları ve istenen çıktıyı açıkça yazar; Codex salt okunur çalışır.
- Çağrı: `Get-Content prompt.md -Raw | codex exec -m gpt-6.1-sol -s read-only -c model_reasoning_effort=<effort> -C <repo> -o answer.md -`.
  Effort kullanıcının kararıdır; verilmediyse sorulur.
- Codex'in cevabı tavsiyedir, karar değildir. Agent bulguları kullanıcıya özetler;
  hangisinin kabul, hangisinin ret edildiğini kullanıcı söyler ve bu, `AI_USAGE.md`'ye işlenir.
- Codex erişilemezse veya model reddedilirse durum kullanıcıya söylenir; başka
  modele sessizce geçilmez, danışma yapılmış gibi yazılmaz.

## Gereksinimden doğan zorunlu davranışlar

Aşağıdakiler PDF'in doğrudan sonucudur; hangi mekanizmayla sağlanacakları
kullanıcının kararıdır. Agent bunlardan birini bozacak bir uygulama fark ederse
kullanıcıya söyler.

- Takım izolasyonu sunucu tarafında, agent/tool'a erişen her yolda denetlenir (R6).
- Model anahtarları şifreli saklanır; log, kayıt ve canlı akışta görünmez; anahtar
  yoksa açık hata, başkasının anahtarına düşme yok (R7).
- Tool'un kendi API anahtarı modelin gördüğü hiçbir yere (şema, mesaj, tool
  sonucu) girmez (R2).
- Bilinmeyen tool, şema ihlali, bozuk JSON ve kaçak döngüler koşuyu çökertmez (R3).
- Her koşu provider hatası, timeout ve sayfa kapatmada da `succeeded`, `failed`
  veya `cancelled` ile biter (R4).
- "requires approval" tool'u onaysız çalışmaz; bekleyen onay yeniden başlatmadan
  sağ çıkar (R5).
- 429'da düzgün davranılır; her model çağrısının token kullanımı kaydedilir (R8).
- Hiçbir anahtar veya şifre repoya girmez — geçmiş dahil (PDF "Secrets").

## Test ve doğrulama

- Test komutu dil kararından sonra burada ve `CLAUDE.md`'de yazılır.
- PDF'in zorunlu tuttuğu test alanları: agent döngüsü, tool şablonları, takım
  yetkileri, onay akışı. Test yaklaşımı (sahte provider, test sunucuları, kapsam)
  kullanıcının kararıdır; agent önerebilir.
- Agent, yazdığı kodun testlerini çalıştırır ve sonucu olduğu gibi raporlar:
  geçen, kalan, atlanan ve nedeni. Çalıştırılmamış test "geçti" sayılmaz.
- Testler gerçek anahtar veya ücretli API çağrısı gerektirmemelidir; gerçek
  modelle uçtan uca koşu ayrı ve manueldir (kullanıcının OpenAI anahtarıyla).
- README'deki kurulum temiz bir klonda baştan sona denenmeden "10 dakikada
  çalışıyor" denmez.

## Çalışmaya başlarken

1. `git status --short --branch`, `PLAN.md` (kararlar, gereksinim durumu,
   sıradaki iş) ve görevle ilgili kod/testleri oku.
2. Görev, kayıtlı bir karara dayanmıyorsa koda başlamadan önce gereken kararı sor.
3. Kullanıcının kirli dosyalarını koru; stash/reset ile gizleme, kendine mal etme.

## Alt agent kullanımı

- Alt agent yalnız uygulama işinde ve görevle orantılı kullanılır; karar vermez,
  kullanıcının kararını değiştirmez. Devirde okunacak kaynaklar, yazılabilecek
  dosyalar, uyulacak kararlar ve kabul kanıtı açıktır; aynı dosyaya eşzamanlı yazma yoktur.
- Alt agent çıktısı tamamlanma kanıtı değildir; ana agent değişikliği ve testleri
  inceleyip kullanıcıya raporlar.

## Dokümantasyon haritası ve oturumlar arası devam

Bilgi tek asıl yerine yazılır; diğer belgelerde kısa özet ve bağlantı kullanılır.
Planlanan iş, uygulanmış davranış ve doğrulanmış sonuç ayrı yazılır. "(planlanan)"
işaretli dosyalar henüz yoktur; oluşturulunca işaret kaldırılır.

| Bilgi | Asıl kayıt yeri |
| --- | --- |
| Agent kuralları ve doküman haritası | `AGENTS.md` (bu dosya) |
| Claude Code giriş noktası | `CLAUDE.md`; kural içermez, `@AGENTS.md` import eder |
| Kullanıcının kararları, planlama batch'leri, gereksinim durumu (R1–R8, EA, teslimatlar), sıradaki iş, engeller | `PLAN.md` |
| Kurulum, çalıştırma, provider/model, örnek agent, varsayımlar | `README.md` (planlanan) |
| Mimari, kararlar/alternatifler, dışarıda bırakılanlar, güvenlik riskleri, ölçek | `DESIGN.md` (≤2 sayfa; yalnız `PLAN.md`'deki kararlardan derlenir) |
| Codex danışmalarının istem ve cevapları | `docs/reviews/<YYYY-MM-DD>-<batch>/` (`prompt.md`, `answer.md`; ham `codex-run.log` git-ignored) |
| AI araç kullanımı, reddedilen/düzeltilen öneriler | `AI_USAGE.md` |
| Yerel geliştirme sırları | `.env` (git-ignored; değişken adları SDK'ların otomatik okuduğu adlardan bilerek farklı) |
| Kayıtlı örnek koşu | `examples/` (planlanan) |

- Yeni bir doküman yalnız kullanıcı isterse eklenir; eklenince bu haritaya aynı
  değişiklikte yolu ve amacı yazılır.
- Oturum sonunda `PLAN.md`'de son başarılı adım, çalıştırılan testler ve sonucu,
  açık sorunlar, bekleyen kararlar ve sıradaki somut iş bulunur. Commit/push
  yapılmadıysa yapılmış gibi yazılmaz. Kritik bilgi yalnız sohbette bırakılmaz.

### AI_USAGE.md'yi canlı tutmak

Case AI kullanımını bekliyor ve **hangi önerilerin reddedildiğini veya
düzeltildiğini** soruyor. Bu yüzden iş sırasında, sonradan hatırlamaya
bırakılmadan `AI_USAGE.md`'ye kısa kayıt düşülür:

- hangi araç/model hangi işte kullanıldı;
- kullanıcının reddettiği veya düzelttiği öneri: ne önerildi, neden reddedildi,
  yerine ne yapıldı;
- kararların kullanıcıya ait olduğu, agent'ın uygulayıcı olduğu.

Kayıt dürüsttür: yapılmamış inceleme veya kullanılmamış araç yazılmaz.

## Git ve sırlar

- **Hiçbir anahtar veya şifre repoya girmez — geçmiş dahil.** Her commit'ten önce
  staged diff sır açısından gözden geçirilir (`git diff --cached`); `examples/`
  kaydı commit'lenmeden önce anahtar, token ve şifre için taranır.
- Sır yanlışlıkla commit'lenirse kullanıcıya hemen söylenir; anahtarı kullanıcı
  geçersiz kılar, geçmişten temizleme yalnız kullanıcı onayıyla yapılır.
- Commit ve push yalnız kullanıcı istediğinde. Force push ve geçmiş yeniden yazımı
  yok (yukarıdaki istisna hariç). Değerlendiriciler geçmişi okuyacağı için commit'ler
  tek anlaşılır amaç taşır; ilgisiz temizlik yoktur.
- Handoff'ta ne değişti, ne çalıştırıldı, ne doğrulanamadı ve hangi kararlar
  bekliyor açık olur. Bitmeyen yerde "production-ready" denmez.
