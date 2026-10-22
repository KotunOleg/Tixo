package i18n

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

type Lang string

const (
	UK      Lang = "uk"
	EN      Lang = "en"
	Default      = UK

	DismissCookie = "tixo_lang_hint_dismissed"
	enPrefix      = "/en"
)

var Supported = []Lang{UK, EN}

type Translator struct {
	localizers map[Lang]*goi18n.Localizer
}

func New(fsys fs.FS) (*Translator, error) {
	bundle := goi18n.NewBundle(language.Ukrainian)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)

	ids := make(map[Lang][]string, len(Supported))
	for _, lang := range Supported {
		mf, err := bundle.LoadMessageFileFS(fsys, "locales/"+string(lang)+".toml")
		if err != nil {
			return nil, fmt.Errorf("i18n: load %s: %w", lang, err)
		}
		for _, m := range mf.Messages {
			ids[lang] = append(ids[lang], m.ID)
		}
		slices.Sort(ids[lang])
	}
	if err := sameKeys(ids); err != nil {
		return nil, err
	}

	t := &Translator{localizers: make(map[Lang]*goi18n.Localizer, len(Supported))}
	for _, lang := range Supported {
		t.localizers[lang] = goi18n.NewLocalizer(bundle, string(lang))
	}
	return t, nil
}

func sameKeys(ids map[Lang][]string) error {
	ref := ids[Default]
	for _, lang := range Supported {
		if lang == Default {
			continue
		}
		var missing []string
		for _, id := range ref {
			if _, found := slices.BinarySearch(ids[lang], id); !found {
				missing = append(missing, fmt.Sprintf("%s missing in %s", id, lang))
			}
		}
		for _, id := range ids[lang] {
			if _, found := slices.BinarySearch(ref, id); !found {
				missing = append(missing, fmt.Sprintf("%s missing in %s", id, Default))
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("i18n: locale keys differ: %s", strings.Join(missing, "; "))
		}
	}
	return nil
}

func (t *Translator) T(lang Lang, id string, kv ...any) (string, error) {
	data, err := pairs(kv)
	if err != nil {
		return "", fmt.Errorf("i18n %s: %w", id, err)
	}
	return t.localize(lang, &goi18n.LocalizeConfig{MessageID: id, TemplateData: data})
}

func (t *Translator) N(lang Lang, id string, count int) (string, error) {
	return t.localize(lang, &goi18n.LocalizeConfig{
		MessageID:    id,
		PluralCount:  count,
		TemplateData: map[string]any{"Count": count},
	})
}

func (t *Translator) localize(lang Lang, cfg *goi18n.LocalizeConfig) (string, error) {
	l, ok := t.localizers[lang]
	if !ok {
		l = t.localizers[Default]
	}
	s, err := l.Localize(cfg)
	if err != nil {
		return "", fmt.Errorf("i18n %s/%s: %w", lang, cfg.MessageID, err)
	}
	return s, nil
}

func pairs(kv []any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("odd number of template arguments")
	}
	data := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("template argument key %v is not a string", kv[i])
		}
		data[k] = kv[i+1]
	}
	return data, nil
}

type ctxKey struct{}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := Default
		if p := r.URL.Path; p == enPrefix || strings.HasPrefix(p, enPrefix+"/") {
			lang = EN
			r = r.Clone(r.Context())
			r.URL.Path = strings.TrimPrefix(p, enPrefix)
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			r.URL.RawPath = ""
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, lang)))
	})
}

func FromContext(ctx context.Context) Lang {
	if lang, ok := ctx.Value(ctxKey{}).(Lang); ok {
		return lang
	}
	return Default
}

func Path(lang Lang, path string) string {
	if lang == EN {
		return enPrefix + path
	}
	return path
}

var matcher = language.NewMatcher([]language.Tag{language.Ukrainian, language.English})

func SuggestEnglish(r *http.Request, current Lang) bool {
	if current != UK {
		return false
	}
	if _, err := r.Cookie(DismissCookie); err == nil {
		return false
	}
	header := r.Header.Get("Accept-Language")
	if header == "" {
		return false
	}
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return false
	}
	_, idx, conf := matcher.Match(tags...)
	return idx == 1 && conf != language.No
}
