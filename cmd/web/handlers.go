package main

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"subscription-project/data"
	"time"

	"github.com/phpdave11/gofpdf"
	"github.com/phpdave11/gofpdf/contrib/gofpdi"
)

func (app *Config) Homepage(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "home.page.gohtml", nil)

}

func (app *Config) LoginPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "login.page.gohtml", nil)
}

func (app *Config) PostLoginPage(w http.ResponseWriter, r *http.Request) {

	err := r.ParseForm()
	if err != nil {
		app.ErrorLog.Println(err)
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	user, err := app.Models.User.GetByEmail(email)

	if err != nil {
		app.Session.Put(r.Context(), "flash", "Invalid credentials")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	validPassword, err := user.PasswordMatches(password)
	if err != nil {
		app.Session.Put(r.Context(), "flash", "Invalid credentials")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if !validPassword {
		msg := Message{
			To:      email,
			Subject: "Failed Login Attempt",
			Data:    "Invalid Login Attempt",
		}

		app.sendMail(msg)

		app.Session.Put(r.Context(), "error", "Invalid credentials")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	//log user in
	app.Session.Put(r.Context(), "userID", user.ID)
	app.Session.Put(r.Context(), "user", user)

	app.Session.Put(r.Context(), "flash", "You've been logged in")
	//redirect the user
	http.Redirect(w, r, "/", http.StatusSeeOther)

}

func (app *Config) Logout(w http.ResponseWriter, r *http.Request) {

	//clean up session
	_ = app.Session.Destroy(r.Context())
	_ = app.Session.RenewToken(r.Context())

	http.Redirect(w, r, "/login", http.StatusSeeOther)

}

func (app *Config) RegisterPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "register.page.gohtml", nil)
}

func (app *Config) PostRegister(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		app.ErrorLog.Println(err)
	}

	//TODO - validate data

	//create a new user
	u := data.User{
		Email:     r.Form.Get("email"),
		FirstName: r.Form.Get("first-name"),
		LastName:  r.Form.Get("last-name"),
		Password:  r.Form.Get("password"),
		Active:    0,
		IsAdmin:   0,
	}
	_, err = u.Insert(u)
	if err != nil {
		app.Session.Put(r.Context(), "error", "An error occurred")
		http.Redirect(w, r, "/register", http.StatusSeeOther)
		return
	}

	//send an activation email

	url := fmt.Sprintf("http://localhost/activate?email=%s", u.Email)
	signedURL := GenerateTokenFromString(url)
	app.InfoLog.Println(signedURL)

	msg := Message{
		To:       u.Email,
		Subject:  "Activate your account",
		Template: "confirmation-email",
		Data:     template.HTML(signedURL),
	}

	app.sendMail(msg)

	app.Session.Put(r.Context(), "flash", "An activation email has been sent to your email")

	http.Redirect(w, r, "/login", http.StatusSeeOther)

}

func (app *Config) ActivateAccount(w http.ResponseWriter, r *http.Request) {
	url := r.RequestURI

	testURL := fmt.Sprintf("http://localhost%s", url)
	okay := VerifyToken(testURL)
	if !okay {
		app.Session.Put(r.Context(), "error", "Invalid token")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	//activate account

	u, err := app.Models.User.GetByEmail(r.URL.Query().Get("email"))

	if err != nil {
		app.Session.Put(r.Context(), "error", "No user found")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	u.Active = 1
	err = u.Update()
	if err != nil {
		app.Session.Put(r.Context(), "error", "Unable to update user")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	app.Session.Put(r.Context(), "flash", "Account activated, You can login now")
	http.Redirect(w, r, "/login", http.StatusSeeOther)

}

func (app *Config) ChooseSubscription(w http.ResponseWriter, r *http.Request) {

	plans, err := app.Models.Plan.GetAll()
	if err != nil {
		app.ErrorLog.Println(err)
		return
	}

	dataMap := make(map[string]any)
	dataMap["plans"] = plans
	app.render(w, r, "plans.page.gohtml", &TemplateData{Data: dataMap})

}

func (app *Config) SubscribeToPlan(w http.ResponseWriter, r *http.Request) {
	//get the id of the plan

	id := r.URL.Query().Get("id")

	planId, _ := strconv.Atoi(id)

	//get the plan from the database
	plan, err := app.Models.Plan.GetOne(planId)

	if err != nil {
		app.Session.Put(r.Context(), "error", "Unable to find plan")
		http.Redirect(w, r, "/members/plans", http.StatusSeeOther)
		return
	}

	//get user from session

	user, ok := app.Session.Get(r.Context(), "user").(data.User)

	if !ok {
		app.Session.Put(r.Context(), "error", "Log In First")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	//send an email with invoice attached

	app.Wait.Add(1)

	go func() {
		defer app.Wait.Done()
		invoice, err := app.getInvoice(user, plan)
		if err != nil {
			//send this to a channel
			app.ErrorChan <- err
		}
		msg := Message{
			To:       user.Email,
			Subject:  "Your Invoice",
			Data:     invoice,
			Template: "invoice",
		}
		app.sendMail(msg)
	}()

	//generate a manual

	app.Wait.Add(1)

	go func() {
		defer app.Wait.Done()

		pdf := app.generateManual(user, plan)
		err := pdf.OutputFileAndClose(fmt.Sprintf("./tmp/%d_manual.pdf", user.ID))
		if err != nil {
			app.ErrorChan <- err
			return
		}
		msg := Message{
			To:      user.Email,
			Subject: "Your Manual",
			Data:    "Your user manual is attached",
			AttachementMap: map[string]string{
				"Manual.pdf": fmt.Sprintf("./tmp/%d_manual.pdf", user.ID),
			},
		}
		// send an email with a manual attached
		app.sendMail(msg)

		//test app error chan

	}()

	//subscribe the user to plan

	err = app.Models.Plan.SubscribeUserToPlan(user, *plan)
	if err != nil {
		app.Session.Put(r.Context(), "error", "Error subscribing to plan")
		http.Redirect(w, r, "/members/plan", http.StatusSeeOther)
		return
	}

	u, err := app.Models.User.GetOne(user.ID)
	if err != nil {
		app.Session.Put(r.Context(), "error", "Error getting user")
		http.Redirect(w, r, "/members/plan", http.StatusSeeOther)
		return
	}

	app.Session.Put(r.Context(), "user", u)

	//redirect
	app.Session.Put(r.Context(), "flash", "Subscribed!")
	http.Redirect(w, r, "/members/plans", http.StatusSeeOther)

}

func (app *Config) getInvoice(u data.User, paln *data.Plan) (string, error) {
	return paln.PlanAmountFormatted, nil
}

func (app *Config) generateManual(u data.User, plan *data.Plan) *gofpdf.Fpdf {
	pdf := gofpdf.New("P", "mm", "Letter", "")
	pdf.SetMargins(10, 13, 10)

	importer := gofpdi.NewImporter()

	time.Sleep(time.Second * 5)

	t := importer.ImportPage(pdf, "./pdf/manual.pdf", 1, "/MediaBox")

	pdf.AddPage()

	importer.UseImportedTemplate(pdf, t, 0, 0, 215.9, 0)
	pdf.SetX(75)
	pdf.SetY(150)
	pdf.SetFont("Arial", "", 12)
	pdf.MultiCell(0, 4, fmt.Sprintf("%s %s", u.FirstName, u.LastName), "", "C", false)
	pdf.Ln(5)
	pdf.MultiCell(0, 4, fmt.Sprintf("%s User guide", plan.PlanName), "", "C", false)

	return pdf
}
