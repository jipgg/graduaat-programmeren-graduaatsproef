THESIS   := gradproef/GijsensJippeBP.tex
VOORSTEL := voorstel/FamilienaamVoornaam-BPvoorstel.tex
OUTDIR   := output

LATEXMK  := latexmk -xelatex -shell-escape -interaction=nonstopmode \
             -file-line-error -synctex=1 -output-directory=../$(OUTDIR)

.PHONY: thesis voorstel poster all clean deps

all: thesis

thesis: $(OUTDIR)
	cd gradproef && $(LATEXMK) GijsensJippeBP.tex

voorstel: $(OUTDIR)
	cd voorstel && $(LATEXMK) FamilienaamVoornaam-BPvoorstel.tex

poster: $(OUTDIR)
	cd poster && $(LATEXMK) conference_poster.tex

$(OUTDIR):
	mkdir -p $(OUTDIR)

clean:
	rm -rf $(OUTDIR)

deps:
	@echo "Installing missing dependencies..."
	sudo pacman -S --noconfirm biber python-pygments
